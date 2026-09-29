// Package client provides a gRPC client for the Nyttig daemon.
//
// The Client struct wraps all Nyttig service RPCs and exposes StreamItems
// as a Go channel that the TUI can consume. It supports connection over
// Unix domain sockets (local) and TCP+TLS (remote).
//
// Reconnection is handled transparently: when the underlying gRPC connection
// drops, the client will attempt to reconnect with exponential backoff.
package client

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/sonhal/nyttig/internal/mtls"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// Options configures the client connection.
type Options struct {
	// Addr is the daemon address.
	// For Unix socket: "unix:///tmp/nyttig.sock"
	// For TCP:          "localhost:9090"
	// For TCP+TLS:      "example.com:9090" (with TLSConfig set)
	Addr string

	// Reconnect enables automatic reconnection on connection failure.
	// Defaults to true.
	Reconnect bool

	// MaxReconnectBackoff is the maximum backoff duration between reconnection
	// attempts. Defaults to 30s.
	MaxReconnectBackoff time.Duration

	// StreamBufferSize is the buffer size for the StreamItems output channel.
	// Defaults to 256.
	StreamBufferSize int

	// ── Mutual TLS ──
	// When TLSCert/TLSKey/TLSCA are set, the client connects over mutual TLS:
	// it presents the client certificate and verifies the daemon against the CA.
	// Leave them empty to connect in plaintext (e.g. over a local Unix socket).
	TLSCert string // Client certificate (PEM).
	TLSKey  string // Client private key (PEM).
	TLSCA   string // CA bundle (PEM) used to verify the daemon's certificate.

	// ServerName overrides the name verified against the daemon's certificate.
	// Useful when dialing by IP or through a tunnel where the address does not
	// match the certificate's SAN. Optional.
	ServerName string
}

// tlsOptions projects the client Options onto mtls.ClientOptions.
func (o Options) tlsOptions() mtls.ClientOptions {
	return mtls.ClientOptions{
		CertFile:   o.TLSCert,
		KeyFile:    o.TLSKey,
		CAFile:     o.TLSCA,
		ServerName: o.ServerName,
	}
}

// Client is a gRPC client for the Nyttig daemon.
type Client struct {
	addr string
	opts Options

	mu       sync.RWMutex
	conn     *grpc.ClientConn
	grpc     pb.NyttigClient
	closed   bool
	closeCh  chan struct{}
	closeErr error
	closeMu  sync.Mutex
}

// New creates a new Client with the given options.
// It does not connect until Dial() or any RPC is called.
func New(opts Options) *Client {
	if !opts.Reconnect {
		// Enable reconnect by default.
		opts.Reconnect = true
	}
	if opts.MaxReconnectBackoff == 0 {
		opts.MaxReconnectBackoff = 30 * time.Second
	}
	if opts.StreamBufferSize == 0 {
		opts.StreamBufferSize = 256
	}
	return &Client{
		addr:    opts.Addr,
		opts:    opts,
		closeCh: make(chan struct{}),
	}
}

// target converts the user-friendly addr into a gRPC target.
func (c *Client) target() string {
	if len(c.addr) > 7 && c.addr[:7] == "unix://" {
		return c.addr
	}
	// Default to Unix socket if path-like.
	if c.addr != "" && c.addr[0] == '/' {
		return "unix://" + c.addr
	}
	return c.addr
}

// Dial connects to the daemon. Returns nil if already connected.
func (c *Client) Dial(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	return c.dialLocked(ctx)
}

// dialLocked establishes the gRPC connection. Must hold c.mu.
func (c *Client) dialLocked(ctx context.Context) error {
	target := c.target()

	var creds credentials.TransportCredentials
	if c.opts.tlsOptions().Enabled() {
		tc, err := mtls.ClientCredentials(c.opts.tlsOptions())
		if err != nil {
			return err
		}
		creds = tc
	} else {
		creds = insecure.NewCredentials()
	}
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}
	// gRPC applies HTTPS_PROXY to every target, but a proxy can never reach
	// a local Unix socket, so dial it directly.
	if strings.HasPrefix(target, "unix:") {
		dialOpts = append(dialOpts, grpc.WithNoProxy())
	}

	conn, err := grpc.DialContext(ctx, target, dialOpts...)
	if err != nil {
		return fmt.Errorf("dial %s: %w", target, err)
	}

	c.conn = conn
	c.grpc = pb.NewNyttigClient(conn)
	return nil
}

// Close shuts down the gRPC connection and all active streams.
func (c *Client) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return c.closeErr
	}
	c.closed = true
	close(c.closeCh)
	c.closeMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		c.grpc = nil
		return err
	}
	return nil
}

// ensureConn dials if not yet connected, with reconnection support.
func (c *Client) ensureConn(ctx context.Context) error {
	c.closeMu.Lock()
	closed := c.closed
	c.closeMu.Unlock()
	if closed {
		return fmt.Errorf("client closed")
	}

	c.mu.RLock()
	connected := c.conn != nil
	c.mu.RUnlock()

	if connected {
		return nil
	}

	return c.Dial(ctx)
}

// reconnectLoop tries to re-establish connection with exponential backoff.
func (c *Client) reconnectLoop() {
	backoff := 100 * time.Millisecond
	maxBackoff := c.opts.MaxReconnectBackoff

	for {
		c.closeMu.Lock()
		closed := c.closed
		c.closeMu.Unlock()
		if closed {
			return
		}

		c.mu.Lock()
		if c.conn != nil {
			c.mu.Unlock()
			return // reconnected
		}
		// Still not connected; try dialing.
		c.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := c.Dial(ctx)
		cancel()
		if err == nil {
			return
		}

		select {
		case <-c.closeCh:
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// ── Unary RPC wrappers ────────────────────────────────────────

// AddSource creates a new feed source.
func (c *Client) AddSource(ctx context.Context, req *pb.AddSourceRequest) (*pb.Source, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	return client.AddSource(ctx, req)
}

// RemoveSource removes a feed source and all its items (cascading).
func (c *Client) RemoveSource(ctx context.Context, id int64) error {
	if err := c.ensureConn(ctx); err != nil {
		return err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	_, err := client.RemoveSource(ctx, &pb.RemoveSourceRequest{Id: id})
	return err
}

// UpdateSource updates a feed source's properties.
func (c *Client) UpdateSource(ctx context.Context, req *pb.UpdateSourceRequest) (*pb.Source, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	return client.UpdateSource(ctx, req)
}

// ListSources returns all configured feed sources.
func (c *Client) ListSources(ctx context.Context) (*pb.ListSourcesResponse, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	return client.ListSources(ctx, &emptypb.Empty{})
}

// RefreshSource triggers an immediate fetch for a specific source.
// Pass sourceID=0 to refresh all sources.
func (c *Client) RefreshSource(ctx context.Context, sourceID int64) error {
	if err := c.ensureConn(ctx); err != nil {
		return err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	_, err := client.RefreshSource(ctx, &pb.RefreshSourceRequest{SourceId: sourceID})
	return err
}

// AddTag creates a new tag.
func (c *Client) AddTag(ctx context.Context, req *pb.AddTagRequest) (*pb.Tag, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	return client.AddTag(ctx, req)
}

// RemoveTag removes a tag and all its associations (cascading).
func (c *Client) RemoveTag(ctx context.Context, id int64) error {
	if err := c.ensureConn(ctx); err != nil {
		return err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	_, err := client.RemoveTag(ctx, &pb.RemoveTagRequest{Id: id})
	return err
}

// ListTags returns all defined tags.
func (c *Client) ListTags(ctx context.Context) (*pb.ListTagsResponse, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	return client.ListTags(ctx, &emptypb.Empty{})
}

// AddTagRule creates a new tag rule (regex pattern).
func (c *Client) AddTagRule(ctx context.Context, req *pb.AddTagRuleRequest) (*pb.TagRule, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	return client.AddTagRule(ctx, req)
}

// RemoveTagRule removes a tag rule.
func (c *Client) RemoveTagRule(ctx context.Context, id int64) error {
	if err := c.ensureConn(ctx); err != nil {
		return err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	_, err := client.RemoveTagRule(ctx, &pb.RemoveTagRuleRequest{Id: id})
	return err
}

// ListTagRules returns all defined tag rules.
func (c *Client) ListTagRules(ctx context.Context) (*pb.ListTagRulesResponse, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	return client.ListTagRules(ctx, &emptypb.Empty{})
}

// Search performs an FTS5 full-text search.
func (c *Client) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	return client.Search(ctx, req)
}

// MarkViewed marks items as viewed (K9s-style scroll-past tracking).
func (c *Client) MarkViewed(ctx context.Context, itemIDs []int64) error {
	if err := c.ensureConn(ctx); err != nil {
		return err
	}
	c.mu.RLock()
	client := c.grpc
	c.mu.RUnlock()
	_, err := client.MarkViewed(ctx, &pb.MarkViewedRequest{ItemIds: itemIDs})
	return err
}

// ── StreamItems channel-based API ──────────────────────────────

// StreamSub represents an active StreamItems subscription.
// The Messages channel receives ServerMessage protos from the daemon.
// SendFilter sends a new filter to the server mid-stream.
// Err() returns any error that terminated the stream.
// Close terminates the subscription.
type StreamSub struct {
	Messages   <-chan *pb.ServerMessage
	sendFilter chan<- *pb.StreamFilter
	errCh      <-chan error
	ctx        context.Context
	cancel     context.CancelFunc
}

// SendFilter sends a new filter to the server, causing a reset and re-send
// of matching items. Non-blocking: if the send buffer is full it drops silently
// (the client can send another filter later).
func (s *StreamSub) SendFilter(filter *pb.StreamFilter) {
	select {
	case s.sendFilter <- filter:
	default:
		// Buffer full; the latest filter will be picked up next.
	}
}

// Err returns any error that caused the stream to terminate, or nil if
// the stream is still active. Blocks until stream ends, then returns.
func (s *StreamSub) Err() error {
	select {
	case err := <-s.errCh:
		return err
	default:
		return nil
	}
}

// Close terminates the StreamItems subscription.
func (s *StreamSub) Close() {
	s.cancel()
}

// NewStreamSubForTest creates a StreamSub backed by a user-supplied channel.
// This is only for use in tests (e.g., testing the TUI's ListenStream function).
func NewStreamSubForTest(msgCh chan *pb.ServerMessage, streamErr error) *StreamSub {
	errCh := make(chan error, 1)
	if streamErr != nil {
		errCh <- streamErr
	}
	return &StreamSub{
		Messages:   msgCh,
		sendFilter: make(chan *pb.StreamFilter, 1),
		errCh:      errCh,
		ctx:        context.Background(),
		cancel:     func() {},
	}
}

// StreamItems initiates a bidirectional streaming RPC and returns a StreamSub.
//
// The initial filter is sent immediately. Messages from the server (Item,
// Reset, Complete) are delivered on the Messages channel. Call SendFilter()
// on the returned StreamSub to update filters mid-stream — the server will
// send a Reset message followed by new matching items and a Complete.
//
// The stream runs in a background goroutine. If the connection drops and
// Reconnect is enabled, the client will attempt to reconnect and re-establish
// the stream with the most recent filter.
func (c *Client) StreamItems(ctx context.Context, initialFilter *pb.StreamFilter) (*StreamSub, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}

	subCtx, cancel := context.WithCancel(ctx)

	msgCh := make(chan *pb.ServerMessage, c.opts.StreamBufferSize)
	filterCh := make(chan *pb.StreamFilter, 2)
	errCh := make(chan error, 1)

	sub := &StreamSub{
		Messages:   msgCh,
		sendFilter: filterCh,
		errCh:      errCh,
		ctx:        subCtx,
		cancel:     cancel,
	}

	// Send initial filter with a small buffer so it doesn't block startup.
	go func() {
		select {
		case filterCh <- initialFilter:
		case <-subCtx.Done():
		}
	}()

	go c.runStream(subCtx, msgCh, filterCh, errCh)

	return sub, nil
}

// runStream is the background goroutine that manages the StreamItems bidirectional stream.
func (c *Client) runStream(ctx context.Context, msgCh chan<- *pb.ServerMessage, filterCh <-chan *pb.StreamFilter, errCh chan<- error) {
	defer close(msgCh)

	var latestFilter *pb.StreamFilter

	// drainFilterCh reads any pending filter from filterCh (non-blocking).
	drainFilterCh := func(current *pb.StreamFilter) *pb.StreamFilter {
		for {
			select {
			case f := <-filterCh:
				current = f
			default:
				return current
			}
		}
	}

	// Read the initial filter.
	select {
	case f := <-filterCh:
		latestFilter = f
	case <-ctx.Done():
		errCh <- ctx.Err()
		return
	}

	for {
		if err := c.streamOnce(ctx, latestFilter, msgCh, filterCh); err != nil {
			// Check if there's a newer filter we should use on reconnect.
			latestFilter = drainFilterCh(latestFilter)

			// If context is done, report and exit.
			if ctx.Err() != nil {
				errCh <- ctx.Err()
				return
			}

			// If reconnection is disabled, report error and exit.
			if !c.opts.Reconnect {
				errCh <- err
				return
			}

			// Attempt reconnection.
			c.mu.Lock()
			if c.conn != nil {
				c.conn.Close()
				c.conn = nil
				c.grpc = nil
			}
			c.mu.Unlock()

			c.reconnectLoop()

			// Check if reconnected or closed.
			c.closeMu.Lock()
			closed := c.closed
			c.closeMu.Unlock()
			if closed {
				errCh <- fmt.Errorf("client closed during reconnect")
				return
			}

			// If still not connected, exit.
			c.mu.RLock()
			connected := c.conn != nil
			c.mu.RUnlock()
			if !connected {
				errCh <- fmt.Errorf("failed to reconnect: %w", err)
				return
			}

			// Send a reset-like message so the consumer knows data is fresh.
			select {
			case msgCh <- &pb.ServerMessage{Msg: &pb.ServerMessage_Reset_{Reset_: &pb.Reset{}}}:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}

			// Continue to re-establish the stream.
			continue
		}
		// streamOnce returned nil = clean shutdown.
		errCh <- nil
		return
	}
}

// streamOnce opens a single StreamItems call, sends the initial filter, and
// reads server messages until the stream ends or an error occurs.
func (c *Client) streamOnce(ctx context.Context, filter *pb.StreamFilter, msgCh chan<- *pb.ServerMessage, filterCh <-chan *pb.StreamFilter) error {
	c.mu.RLock()
	if c.grpc == nil {
		c.mu.RUnlock()
		return fmt.Errorf("not connected")
	}
	stream, err := c.grpc.StreamItems(ctx)
	c.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("create stream: %w", err)
	}

	// Send initial filter.
	if err := stream.Send(&pb.ClientMessage{
		Msg: &pb.ClientMessage_Filter{Filter: filter},
	}); err != nil {
		return fmt.Errorf("send filter: %w", err)
	}

	// Use a sub-context so we can cancel the partner goroutine when one fails.
	onceCtx, onceCancel := context.WithCancel(ctx)
	defer onceCancel()

	streamErr := make(chan error, 1)

	// Recv goroutine: reads ServerMessages from the stream and pushes to msgCh.
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				streamErr <- err
				return
			}

			select {
			case msgCh <- msg:
			case <-onceCtx.Done():
				return
			}
		}
	}()

	// Main goroutine: reads filter changes and sends them, or detects stream error.
	for {
		select {
		case err := <-streamErr:
			// Recv goroutine ended; clean up.
			stream.CloseSend()
			return err
		case f, ok := <-filterCh:
			if !ok {
				stream.CloseSend()
				return fmt.Errorf("filter channel closed")
			}
			if err := stream.Send(&pb.ClientMessage{
				Msg: &pb.ClientMessage_Filter{Filter: f},
			}); err != nil {
				return fmt.Errorf("send filter: %w", err)
			}
		case <-onceCtx.Done():
			return onceCtx.Err()
		}
	}
}
