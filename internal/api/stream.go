package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── SSE bridge ────────────────────────────────────────────────
//
// One gRPC StreamItems call per SSE connection. The filter comes from the
// query string and never changes: to change it the browser closes the
// EventSource and opens a new one.
//
// Events:
//
//	event: reset      a snapshot follows; the client starts a new buffer
//	event: item       id: <item id>, data: protojson Item
//	event: complete   the snapshot is done; later items are live pushes
//	: ping            comment every pingInterval so proxies keep the line open
//
// The daemon's Hub drops pushes for subscribers that fall behind, so the
// gRPC stream is read as fast as it arrives into a bounded queue. If the
// browser cannot keep up and the queue fills, the connection is closed:
// the browser reconnects and resyncs from a fresh snapshot instead of
// silently missing items.

const (
	defaultPingInterval = 20 * time.Second
	defaultQueueSize    = 1024
	// firstMessageTimeout bounds how long the handler waits for the daemon
	// before it gives up with an HTTP error (rather than an empty stream).
	firstMessageTimeout = 10 * time.Second
	// writeTimeout bounds each write to the browser.
	writeTimeout = 30 * time.Second
	// retryMillis is the reconnect delay suggested to EventSource.
	retryMillis = 3000
)

// errOverflow is the cause when a slow browser overflows the queue.
var errOverflow = errors.New("sse queue overflow")

type streamHandler struct {
	client       pb.NyttigClient
	pingInterval time.Duration
	queueSize    int
	log          *slog.Logger
}

func (h *streamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f, err := parseFeedFilter(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)

	stream, err := h.client.StreamItems(ctx)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	if err := stream.Send(&pb.ClientMessage{Msg: &pb.ClientMessage_Filter{Filter: &pb.StreamFilter{
		SourceId:     f.SourceID,
		TagId:        f.TagID,
		Search:       f.Query,
		Sort:         f.Sort,
		UnviewedOnly: f.UnviewedOnly,
	}}}); err != nil {
		writeRPCError(w, recvError(stream, err))
		return
	}
	// No CloseSend: the daemon ends the stream when the client half closes.

	sse := &sseWriter{w: w, rc: http.NewResponseController(w)}

	// Reader: move messages from gRPC into the bounded queue without ever
	// blocking, so the daemon never sees this subscriber as slow.
	queue := make(chan *pb.ServerMessage, h.queueSize)
	readErr := make(chan error, 1)
	go func() {
		defer close(queue)
		for {
			msg, err := stream.Recv()
			if err != nil {
				readErr <- err
				return
			}
			select {
			case queue <- msg:
			default:
				// The browser is not keeping up. Unblock a write that is
				// stuck on it too, so the connection closes now.
				cancel(errOverflow)
				sse.abort()
				readErr <- errOverflow
				return
			}
		}
	}()

	// Wait for the first message so a daemon that is down turns into an
	// HTTP error the client can see, not an empty event stream.
	var first *pb.ServerMessage
	timer := time.NewTimer(firstMessageTimeout)
	select {
	case msg, ok := <-queue:
		timer.Stop()
		if !ok {
			err := <-readErr
			if err == io.EOF {
				err = status.Error(codes.Unavailable, "daemon closed the stream")
			}
			writeRPCError(w, err)
			return
		}
		first = msg
	case <-timer.C:
		cancel(errors.New("timeout"))
		writeError(w, http.StatusGatewayTimeout, "daemon did not answer")
		return
	case <-r.Context().Done():
		timer.Stop()
		return
	}

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream; charset=utf-8")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sse.raw("retry: " + strconv.Itoa(retryMillis) + "\n\n")
	// Every connection starts a snapshot. The daemon only sends Reset on
	// filter changes, so the bridge sends the first one itself.
	sse.event("reset", "", []byte("{}"))
	sse.message(first)
	if sse.flush() != nil {
		return
	}

	ping := time.NewTicker(h.pingInterval)
	defer ping.Stop()
	for {
		select {
		case msg, ok := <-queue:
			if !ok {
				err := <-readErr
				if errors.Is(err, errOverflow) {
					h.log.Warn("sse: browser too slow, closing stream so it resyncs")
				} else if err != io.EOF && ctx.Err() == nil {
					h.log.Info("sse: daemon stream ended", "error", err)
				}
				return
			}
			sse.message(msg)
			// Drain whatever else is ready before flushing, so a snapshot
			// goes out in a few large writes rather than one per item.
			for drained := false; !drained; {
				select {
				case next, ok := <-queue:
					if !ok {
						drained = true
						break
					}
					sse.message(next)
				default:
					drained = true
				}
			}
			if sse.flush() != nil {
				return
			}
		case <-ping.C:
			sse.raw(": ping\n\n")
			if sse.flush() != nil {
				return
			}
		case <-ctx.Done():
			if errors.Is(context.Cause(ctx), errOverflow) {
				h.log.Warn("sse: browser too slow, closing stream so it resyncs")
			}
			return
		}
	}
}

// recvError prefers the stream's own status (from Recv) over the generic
// io.EOF a failed Send reports.
func recvError(stream pb.Nyttig_StreamItemsClient, sendErr error) error {
	if sendErr != io.EOF {
		return sendErr
	}
	if _, err := stream.Recv(); err != nil {
		return err
	}
	return sendErr
}

// sseWriter frames Server-Sent Events. The first write error sticks and
// makes later writes no-ops.
//
// Only one goroutine writes; abort may be called from another.
type sseWriter struct {
	w   io.Writer
	rc  *http.ResponseController
	err error

	mu      sync.Mutex
	aborted bool
}

// abort makes the current and every later write fail at once.
func (s *sseWriter) abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborted = true
	_ = s.rc.SetWriteDeadline(time.Unix(1, 0))
}

func (s *sseWriter) raw(str string) {
	if s.err != nil {
		return
	}
	s.mu.Lock()
	if s.aborted {
		s.mu.Unlock()
		s.err = errOverflow
		return
	}
	_ = s.rc.SetWriteDeadline(time.Now().Add(writeTimeout))
	s.mu.Unlock()
	_, s.err = io.WriteString(s.w, str)
}

// event writes one event. data must not contain newlines; protojson and
// encoding/json never emit them in compact mode.
func (s *sseWriter) event(name, id string, data []byte) {
	msg := ""
	if id != "" {
		msg += "id: " + id + "\n"
	}
	msg += "event: " + name + "\ndata: " + string(data) + "\n\n"
	s.raw(msg)
}

func (s *sseWriter) message(m *pb.ServerMessage) {
	switch v := m.Msg.(type) {
	case *pb.ServerMessage_Item:
		item := sanitizeItem(v.Item)
		b, err := marshaler.Marshal(item)
		if err != nil {
			s.err = fmt.Errorf("encode item: %w", err)
			return
		}
		s.event("item", strconv.FormatInt(item.Id, 10), b)
	case *pb.ServerMessage_Reset_:
		s.event("reset", "", []byte("{}"))
	case *pb.ServerMessage_Complete:
		s.event("complete", "", []byte("{}"))
	}
}

func (s *sseWriter) flush() error {
	if s.err != nil {
		return s.err
	}
	s.err = s.rc.Flush()
	return s.err
}
