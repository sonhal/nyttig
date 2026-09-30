package api

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// fakeClient implements pb.NyttigClient for handler tests. Methods not
// overridden panic through the nil embedded interface, which flags a
// handler calling an RPC the test did not expect.
type fakeClient struct {
	pb.NyttigClient

	mu        sync.Mutex
	err       error // returned by every unary RPC when set
	sources   []*pb.Source
	tags      []*pb.Tag
	search    *pb.SearchResponse
	lastSrch  *pb.SearchRequest
	refreshed []int64
	viewed    [][]int64

	// stream is returned by StreamItems; streamErr fails the call instead.
	stream    *fakeStream
	streamErr error
}

func (f *fakeClient) ListSources(context.Context, *emptypb.Empty, ...grpc.CallOption) (*pb.ListSourcesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &pb.ListSourcesResponse{Sources: f.sources}, nil
}

func (f *fakeClient) ListTags(context.Context, *emptypb.Empty, ...grpc.CallOption) (*pb.ListTagsResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &pb.ListTagsResponse{Tags: f.tags}, nil
}

func (f *fakeClient) RefreshSource(_ context.Context, req *pb.RefreshSourceRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshed = append(f.refreshed, req.SourceId)
	return &emptypb.Empty{}, nil
}

func (f *fakeClient) Search(_ context.Context, req *pb.SearchRequest, _ ...grpc.CallOption) (*pb.SearchResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSrch = req
	if f.search == nil {
		return &pb.SearchResponse{}, nil
	}
	return proto.Clone(f.search).(*pb.SearchResponse), nil
}

func (f *fakeClient) MarkViewed(_ context.Context, req *pb.MarkViewedRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewed = append(f.viewed, req.ItemIds)
	return &emptypb.Empty{}, nil
}

func (f *fakeClient) StreamItems(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pb.ClientMessage, pb.ServerMessage], error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	f.stream.ctx = ctx
	close(f.stream.started)
	return f.stream, nil
}

// fakeStream is a StreamItems client stream fed from a channel. Closing
// msgs ends the stream with io.EOF; recvErr, when set, ends it with that
// error instead.
type fakeStream struct {
	grpc.ClientStream

	ctx     context.Context
	started chan struct{}
	msgs    chan *pb.ServerMessage
	recvErr error

	mu   sync.Mutex
	sent []*pb.ClientMessage
}

func newFakeStream(buf int) *fakeStream {
	return &fakeStream{started: make(chan struct{}), msgs: make(chan *pb.ServerMessage, buf)}
}

func (s *fakeStream) Send(m *pb.ClientMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, m)
	return nil
}

func (s *fakeStream) Recv() (*pb.ServerMessage, error) {
	select {
	case m, ok := <-s.msgs:
		if !ok {
			if s.recvErr != nil {
				return nil, s.recvErr
			}
			return nil, io.EOF
		}
		return m, nil
	case <-s.ctx.Done():
		return nil, status.FromContextError(s.ctx.Err()).Err()
	}
}

func (s *fakeStream) CloseSend() error             { return nil }
func (s *fakeStream) Context() context.Context     { return s.ctx }
func (s *fakeStream) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeStream) Trailer() metadata.MD         { return nil }

func (s *fakeStream) filter() *pb.StreamFilter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return nil
	}
	return s.sent[0].GetFilter()
}

// ── Helpers ───────────────────────────────────────────────────

const testOrigin = "https://nyttig.example.com"

func newTestHandler(t *testing.T, fc *fakeClient) *testHandler {
	t.Helper()
	h, err := New(Config{Client: fc, Origin: testOrigin})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &testHandler{h: h}
}

var errUnavailable = status.Error(codes.Unavailable, "connection refused")

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
