package service

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// startGRPC serves svc on an in-memory listener and returns a client and the
// server, so a test can use real streams and GracefulStop.
func startGRPC(t *testing.T, svc *Service) (pb.NyttigClient, *grpc.Server) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pb.RegisterNyttigServer(srv, svc)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewNyttigClient(conn), srv
}

// openStream starts StreamItems and waits for the initial Complete, so the
// handler is in its main loop.
func openStream(t *testing.T, ctx context.Context, client pb.NyttigClient) pb.Nyttig_StreamItemsClient {
	t.Helper()
	stream, err := client.StreamItems(ctx)
	if err != nil {
		t.Fatalf("StreamItems: %v", err)
	}
	if err := stream.Send(&pb.ClientMessage{Msg: &pb.ClientMessage_Filter{Filter: &pb.StreamFilter{}}}); err != nil {
		t.Fatalf("send filter: %v", err)
	}
	for {
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv before Complete: %v", err)
		}
		if msg.GetComplete() != nil {
			return stream
		}
	}
}

func TestHubClose_EndsActiveStreams(t *testing.T) {
	svc, _ := newTestService(t)
	client, srv := startGRPC(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	streams := []pb.Nyttig_StreamItemsClient{openStream(t, ctx, client), openStream(t, ctx, client)}

	svc.Hub().Close()
	svc.Hub().Close() // idempotent

	for _, stream := range streams {
		_, err := stream.Recv()
		wantCode(t, err, codes.Unavailable)
	}

	// With the streams gone GracefulStop has nothing to wait for.
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("GracefulStop still blocked after Hub.Close")
	}
}

func TestHubClose_StreamStartedAfterCloseEnds(t *testing.T) {
	svc, _ := newTestService(t)
	client, _ := startGRPC(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc.Hub().Close()

	stream, err := client.StreamItems(ctx)
	if err != nil {
		t.Fatalf("StreamItems: %v", err)
	}
	_ = stream.Send(&pb.ClientMessage{Msg: &pb.ClientMessage_Filter{Filter: &pb.StreamFilter{}}})
	_, err = stream.Recv()
	wantCode(t, err, codes.Unavailable)
}

// fakeStream is a StreamItems server stream driven by the test, so the
// test can watch the handler itself return.
type fakeStream struct {
	grpc.ServerStream
	ctx  context.Context
	in   chan *pb.ClientMessage
	sent chan *pb.ServerMessage
}

func (f *fakeStream) Context() context.Context { return f.ctx }
func (f *fakeStream) Send(m *pb.ServerMessage) error {
	f.sent <- m
	return nil
}
func (f *fakeStream) Recv() (*pb.ClientMessage, error) {
	select {
	case m := <-f.in:
		return m, nil
	case <-f.ctx.Done():
		return nil, f.ctx.Err()
	}
}

// TestHubClose_HandlerReturns runs the handler on a stream whose client stays
// connected and silent, and checks that Close makes it return.
func TestHubClose_HandlerReturns(t *testing.T) {
	svc, _ := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // only reached if the handler is stuck: the client never leaves
	stream := &fakeStream{ctx: ctx, in: make(chan *pb.ClientMessage, 1), sent: make(chan *pb.ServerMessage, 16)}
	stream.in <- &pb.ClientMessage{Msg: &pb.ClientMessage_Filter{Filter: &pb.StreamFilter{}}}

	returned := make(chan error, 1)
	go func() { returned <- svc.StreamItems(stream) }()

	// Wait for the initial snapshot: the handler is now idle in its main loop.
	select {
	case m := <-stream.sent:
		if m.GetComplete() == nil {
			t.Fatalf("first message = %v, want Complete (empty db)", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no initial Complete")
	}
	select {
	case err := <-returned:
		t.Fatalf("handler returned before Close: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	svc.Hub().Close()

	select {
	case err := <-returned:
		wantCode(t, err, codes.Unavailable)
	case <-time.After(2 * time.Second):
		t.Fatal("handler still running 2s after Hub.Close")
	}
}
