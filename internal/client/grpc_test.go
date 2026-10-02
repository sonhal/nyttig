package client

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── Test server (in-process fake) ──────────────────────────────

type testServer struct {
	pb.UnimplementedNyttigServer
	mu      sync.Mutex
	sources []*pb.Source
	tags    []*pb.Tag
	rules   []*pb.TagRule
	items   []*pb.Item
	viewed  map[int64]bool

	assessors []*pb.Assessor
	// assessorRace, when set, is added just before the next AddAssessor
	// answers, as if another client had created it first.
	assessorRace *pb.Assessor

	// StreamItems control
	streamMu sync.Mutex
	streams  []pb.Nyttig_StreamItemsServer
	itemCh   chan *pb.Item
}

func newTestServer() *testServer {
	return &testServer{
		viewed: make(map[int64]bool),
		itemCh: make(chan *pb.Item, 10),
	}
}

func (s *testServer) AddAssessor(ctx context.Context, req *pb.AddAssessorRequest) (*pb.Assessor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Another client creating the same assessor at the same moment.
	if s.assessorRace != nil {
		s.assessors = append(s.assessors, s.assessorRace)
		s.assessorRace = nil
	}
	for _, a := range s.assessors {
		if a.Name == req.Name {
			return nil, status.Error(codes.AlreadyExists, "assessor already exists")
		}
	}
	a := &pb.Assessor{Id: int64(len(s.assessors) + 1), Name: req.Name, Description: req.Description, Color: req.Color}
	s.assessors = append(s.assessors, a)
	return a, nil
}

func (s *testServer) ListAssessors(ctx context.Context, _ *emptypb.Empty) (*pb.ListAssessorsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.ListAssessorsResponse{Assessors: append([]*pb.Assessor(nil), s.assessors...)}, nil
}

func (s *testServer) PutAssessment(ctx context.Context, req *pb.PutAssessmentRequest) (*pb.Assessment, error) {
	return &pb.Assessment{Id: 1, ItemId: req.ItemId, AssessorId: req.AssessorId, TagId: req.TagId, Score: req.Score, Note: req.Note}, nil
}

func (s *testServer) RemoveAssessment(ctx context.Context, req *pb.RemoveAssessmentRequest) (*emptypb.Empty, error) {
	if req.ItemId != 7 {
		return nil, status.Error(codes.NotFound, "assessment not found")
	}
	return &emptypb.Empty{}, nil
}

func (s *testServer) RemoveAssessor(ctx context.Context, req *pb.RemoveAssessorRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (s *testServer) UpdateAssessor(ctx context.Context, req *pb.UpdateAssessorRequest) (*pb.Assessor, error) {
	return &pb.Assessor{Id: req.Id, Name: req.GetName()}, nil
}

func (s *testServer) AddSource(ctx context.Context, req *pb.AddSourceRequest) (*pb.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := &pb.Source{
		Id:         int64(len(s.sources) + 1),
		Name:       req.Name,
		Url:        req.Url,
		Type:       req.Type,
		RefreshSec: req.RefreshSec,
		Enabled:    req.Enabled,
		CreatedAt:  timestamppb.Now(),
	}
	s.sources = append(s.sources, src)
	return src, nil
}

func (s *testServer) RemoveSource(ctx context.Context, req *pb.RemoveSourceRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := make([]*pb.Source, 0, len(s.sources))
	for _, src := range s.sources {
		if src.Id != req.Id {
			filtered = append(filtered, src)
		}
	}
	s.sources = filtered
	return &emptypb.Empty{}, nil
}

func (s *testServer) UpdateSource(ctx context.Context, req *pb.UpdateSourceRequest) (*pb.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, src := range s.sources {
		if src.Id == req.Id {
			if req.Name != nil {
				src.Name = *req.Name
			}
			if req.Url != nil {
				src.Url = *req.Url
			}
			if req.RefreshSec != nil {
				src.RefreshSec = *req.RefreshSec
			}
			if req.Enabled != nil {
				src.Enabled = *req.Enabled
			}
			return src, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "source %d not found", req.Id)
}

func (s *testServer) ListSources(ctx context.Context, _ *emptypb.Empty) (*pb.ListSourcesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.ListSourcesResponse{Sources: s.sources}, nil
}

func (s *testServer) RefreshSource(ctx context.Context, req *pb.RefreshSourceRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (s *testServer) AddTag(ctx context.Context, req *pb.AddTagRequest) (*pb.Tag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tag := &pb.Tag{
		Id:    int64(len(s.tags) + 1),
		Name:  req.Name,
		Color: req.Color,
	}
	s.tags = append(s.tags, tag)
	return tag, nil
}

func (s *testServer) RemoveTag(ctx context.Context, req *pb.RemoveTagRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := make([]*pb.Tag, 0, len(s.tags))
	for _, t := range s.tags {
		if t.Id != req.Id {
			filtered = append(filtered, t)
		}
	}
	s.tags = filtered
	return &emptypb.Empty{}, nil
}

func (s *testServer) ListTags(ctx context.Context, _ *emptypb.Empty) (*pb.ListTagsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.ListTagsResponse{Tags: s.tags}, nil
}

func (s *testServer) AddTagRule(ctx context.Context, req *pb.AddTagRuleRequest) (*pb.TagRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule := &pb.TagRule{
		Id:       int64(len(s.rules) + 1),
		SourceId: req.SourceId,
		TagId:    req.TagId,
		Field:    req.Field,
		Pattern:  req.Pattern,
		Priority: req.Priority,
	}
	s.rules = append(s.rules, rule)
	return rule, nil
}

func (s *testServer) RemoveTagRule(ctx context.Context, req *pb.RemoveTagRuleRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := make([]*pb.TagRule, 0, len(s.rules))
	for _, r := range s.rules {
		if r.Id != req.Id {
			filtered = append(filtered, r)
		}
	}
	s.rules = filtered
	return &emptypb.Empty{}, nil
}

func (s *testServer) ListTagRules(ctx context.Context, _ *emptypb.Empty) (*pb.ListTagRulesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.ListTagRulesResponse{Rules: s.rules}, nil
}

func (s *testServer) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.SearchResponse{Items: s.items, Total: int32(len(s.items))}, nil
}

func (s *testServer) MarkViewed(ctx context.Context, req *pb.MarkViewedRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range req.ItemIds {
		s.viewed[id] = true
	}
	return &emptypb.Empty{}, nil
}

func (s *testServer) StreamItems(stream pb.Nyttig_StreamItemsServer) error {
	s.streamMu.Lock()
	s.streams = append(s.streams, stream)
	s.streamMu.Unlock()

	defer func() {
		s.streamMu.Lock()
		for i, st := range s.streams {
			if st == stream {
				s.streams = append(s.streams[:i], s.streams[i+1:]...)
				break
			}
		}
		s.streamMu.Unlock()
	}()

	// Read initial filter from client.
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	filter := msg.GetFilter()
	_ = filter

	// Send initial batch.
	s.mu.Lock()
	for _, item := range s.items {
		if err := stream.Send(&pb.ServerMessage{
			Msg: &pb.ServerMessage_Item{Item: item},
		}); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()

	if err := stream.Send(&pb.ServerMessage{
		Msg: &pb.ServerMessage_Complete{Complete: &pb.Complete{}},
	}); err != nil {
		return err
	}

	// Read filter changes from the client in a separate goroutine. stream.Recv
	// blocks, so it can't share the loop that drains s.itemCh; gRPC also allows
	// only a single concurrent sender, so all Sends stay in the loop below.
	filterCh := make(chan struct{})
	recvDone := make(chan struct{})
	go func() {
		defer close(recvDone)
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
			select {
			case filterCh <- struct{}{}:
			case <-stream.Context().Done():
				return
			}
		}
	}()

	// Push new items and react to filter changes.
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-recvDone:
			return nil
		case item := <-s.itemCh:
			if err := stream.Send(&pb.ServerMessage{
				Msg: &pb.ServerMessage_Item{Item: item},
			}); err != nil {
				return err
			}
		case <-filterCh:
			// On filter change: reset + re-send matching items.
			if err := stream.Send(&pb.ServerMessage{
				Msg: &pb.ServerMessage_Reset_{Reset_: &pb.Reset{}},
			}); err != nil {
				return err
			}
			s.mu.Lock()
			for _, item := range s.items {
				if err := stream.Send(&pb.ServerMessage{
					Msg: &pb.ServerMessage_Item{Item: item},
				}); err != nil {
					s.mu.Unlock()
					return err
				}
			}
			s.mu.Unlock()
			if err := stream.Send(&pb.ServerMessage{
				Msg: &pb.ServerMessage_Complete{Complete: &pb.Complete{}},
			}); err != nil {
				return err
			}
		}
	}
}

// pushItem sends an item to all connected streams.
func (s *testServer) pushItem(item *pb.Item) {
	s.itemCh <- item
}

// ── Setup helpers ──────────────────────────────────────────────

func setupTest(t *testing.T) (*Client, *testServer, func()) {
	t.Helper()

	// Use a real TCP listener on a random port.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	srv := grpc.NewServer()
	ts := newTestServer()
	pb.RegisterNyttigServer(srv, ts)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("server stopped: %v", err)
		}
	}()

	// Add some initial data.
	ts.sources = []*pb.Source{
		{Id: 1, Name: "HN", Url: "https://news.ycombinator.com/rss", Type: "rss", RefreshSec: 600, Enabled: true, CreatedAt: timestamppb.Now()},
	}
	ts.tags = []*pb.Tag{
		{Id: 1, Name: "rust", Color: "#FF6B35"},
		{Id: 2, Name: "go", Color: "#00ADD8"},
	}
	ts.items = []*pb.Item{
		{Id: 1, SourceId: 1, SourceName: "HN", Title: "Rust 1.85 Released", Link: "https://example.com/rust", Published: timestamppb.Now(), FetchedAt: timestamppb.Now()},
		{Id: 2, SourceId: 1, SourceName: "HN", Title: "Go 1.24 Released", Link: "https://example.com/go", Published: timestamppb.Now(), FetchedAt: timestamppb.Now()},
	}

	addr := fmt.Sprintf("127.0.0.1:%d", lis.Addr().(*net.TCPAddr).Port)

	conn, err := grpc.DialContext(context.Background(), addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial %s: %v", addr, err)
	}

	client := &Client{
		addr:    addr,
		opts:    Options{StreamBufferSize: 64, Reconnect: false},
		closeCh: make(chan struct{}),
		conn:    conn,
		grpc:    pb.NewNyttigClient(conn),
	}

	cleanup := func() {
		client.Close()
		srv.Stop()
	}

	return client, ts, cleanup
}

// ── Tests ──────────────────────────────────────────────────────

func TestClient_AddSource(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	src, err := c.AddSource(ctx, &pb.AddSourceRequest{
		Name: "Lobsters", Url: "https://lobste.rs/rss", Type: "rss", RefreshSec: 1800, Enabled: true,
	})
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if src.Name != "Lobsters" {
		t.Errorf("expected name Lobster, got %s", src.Name)
	}
	if src.Id != 2 {
		t.Errorf("expected id 2, got %d", src.Id)
	}
}

func TestClient_RemoveSource(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	if err := c.RemoveSource(ctx, 1); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}
	ts.mu.Lock()
	sz := len(ts.sources)
	ts.mu.Unlock()
	if sz != 0 {
		t.Errorf("expected 0 sources after removal, got %d", sz)
	}
}

func TestClient_UpdateSource(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	src, err := c.UpdateSource(ctx, &pb.UpdateSourceRequest{
		Id: 1, Name: proto.String("Hacker News"), RefreshSec: proto.Int32(300),
	})
	if err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}
	if src.Name != "Hacker News" {
		t.Errorf("expected name 'Hacker News', got %s", src.Name)
	}
	_ = ts
}

func TestClient_ListSources(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	resp, err := c.ListSources(ctx)
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	if len(resp.Sources) != 1 {
		t.Errorf("expected 1 source, got %d", len(resp.Sources))
	}
	if resp.Sources[0].Name != "HN" {
		t.Errorf("expected HN, got %s", resp.Sources[0].Name)
	}
}

func TestClient_RefreshSource(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	if err := c.RefreshSource(ctx, 0); err != nil {
		t.Fatalf("RefreshSource(all): %v", err)
	}
	if err := c.RefreshSource(ctx, 1); err != nil {
		t.Fatalf("RefreshSource(1): %v", err)
	}
}

func TestClient_Assessments(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	a, err := c.AddAssessor(ctx, &pb.AddAssessorRequest{Name: "claude", Description: "d"})
	if err != nil || a.Name != "claude" {
		t.Fatalf("AddAssessor = %v, %v", a, err)
	}
	if up, err := c.UpdateAssessor(ctx, &pb.UpdateAssessorRequest{Id: 1, Name: proto.String("x")}); err != nil || up.Name != "x" {
		t.Fatalf("UpdateAssessor = %v, %v", up, err)
	}
	list, err := c.ListAssessors(ctx)
	if err != nil || len(list.Assessors) != 1 {
		t.Fatalf("ListAssessors = %v, %v", list, err)
	}
	zero := 0.0
	got, err := c.PutAssessment(ctx, &pb.PutAssessmentRequest{ItemId: 7, AssessorId: 1, Score: &zero, Note: "n"})
	if err != nil || got.Score == nil || *got.Score != 0 || got.Note != "n" {
		t.Fatalf("PutAssessment = %v, %v", got, err)
	}
	if err := c.RemoveAssessment(ctx, &pb.RemoveAssessmentRequest{ItemId: 7, AssessorId: 1}); err != nil {
		t.Fatalf("RemoveAssessment: %v", err)
	}
	if err := c.RemoveAssessment(ctx, &pb.RemoveAssessmentRequest{ItemId: 8, AssessorId: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("RemoveAssessment(missing) = %v, want NotFound", err)
	}
	if err := c.RemoveAssessor(ctx, 1); err != nil {
		t.Fatalf("RemoveAssessor: %v", err)
	}
}

func TestClient_AddTag(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	tag, err := c.AddTag(ctx, &pb.AddTagRequest{Name: "linux", Color: "#FCC624"})
	if err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if tag.Name != "linux" {
		t.Errorf("expected name linux, got %s", tag.Name)
	}
}

func TestClient_RemoveTag(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	if err := c.RemoveTag(ctx, 1); err != nil {
		t.Fatalf("RemoveTag: %v", err)
	}
	ts.mu.Lock()
	sz := len(ts.tags)
	ts.mu.Unlock()
	if sz != 1 {
		t.Errorf("expected 1 tag after removal, got %d", sz)
	}
}

func TestClient_ListTags(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	resp, err := c.ListTags(ctx)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(resp.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(resp.Tags))
	}
}

func TestClient_AddTagRule(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	rule, err := c.AddTagRule(ctx, &pb.AddTagRuleRequest{
		SourceId: 0, TagId: 1, Field: "both", Pattern: "(?i)rust", Priority: 1,
	})
	if err != nil {
		t.Fatalf("AddTagRule: %v", err)
	}
	if rule.Pattern != "(?i)rust" {
		t.Errorf("expected pattern (?i)rust, got %s", rule.Pattern)
	}
}

func TestClient_RemoveTagRule(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()

	// Add a rule first.
	ctx := context.Background()
	_, _ = c.AddTagRule(ctx, &pb.AddTagRuleRequest{
		TagId: 1, Field: "title", Pattern: "rust", Priority: 0,
	})

	if err := c.RemoveTagRule(ctx, 1); err != nil {
		t.Fatalf("RemoveTagRule: %v", err)
	}
	ts.mu.Lock()
	sz := len(ts.rules)
	ts.mu.Unlock()
	if sz != 0 {
		t.Errorf("expected 0 rules after removal, got %d", sz)
	}
}

func TestClient_ListTagRules(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	_, _ = c.AddTagRule(ctx, &pb.AddTagRuleRequest{
		TagId: 1, Field: "title", Pattern: "rust", Priority: 0,
	})

	resp, err := c.ListTagRules(ctx)
	if err != nil {
		t.Fatalf("ListTagRules: %v", err)
	}
	if len(resp.Rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(resp.Rules))
	}
	_ = ts
}

func TestClient_Search(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	resp, err := c.Search(ctx, &pb.SearchRequest{Query: "rust", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("expected 2 items, got %d", resp.Total)
	}
}

func TestClient_MarkViewed(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	if err := c.MarkViewed(ctx, []int64{1, 2}); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if !ts.viewed[1] || !ts.viewed[2] {
		t.Errorf("expected items 1 and 2 to be marked viewed, got %v", ts.viewed)
	}
}

func TestClient_StreamItems_InitialBatch(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()
	_ = ts

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := c.StreamItems(ctx, &pb.StreamFilter{
		Sort:  "newest",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("StreamItems: %v", err)
	}
	defer sub.Close()

	// Should receive initial batch (2 items) then Complete.
	var itemCount int
	var gotComplete bool
	timeout := time.After(2 * time.Second)

	for itemCount < 2 || !gotComplete {
		select {
		case msg, ok := <-sub.Messages:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			switch msg.Msg.(type) {
			case *pb.ServerMessage_Item:
				itemCount++
			case *pb.ServerMessage_Complete:
				gotComplete = true
			}
		case <-timeout:
			t.Fatalf("timeout waiting for messages (got %d items, complete=%v)", itemCount, gotComplete)
		}
	}

	if itemCount != 2 {
		t.Errorf("expected 2 items, got %d", itemCount)
	}
	if !gotComplete {
		t.Error("expected complete message")
	}
}

func TestClient_StreamItems_PushNewItem(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := c.StreamItems(ctx, &pb.StreamFilter{
		Sort:  "newest",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("StreamItems: %v", err)
	}
	defer sub.Close()

	// Drain the initial batch.
	var gotComplete bool
	timeout := time.After(3 * time.Second)
drainLoop:
	for {
		select {
		case msg, ok := <-sub.Messages:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			if _, ok := msg.Msg.(*pb.ServerMessage_Complete); ok {
				gotComplete = true
				break drainLoop
			}
		case <-timeout:
			t.Fatal("timeout waiting for initial complete")
		}
	}
	if !gotComplete {
		t.Fatal("expected complete after initial batch")
	}

	// Push a new item from the server side.
	newItem := &pb.Item{
		Id: 3, SourceId: 1, SourceName: "HN",
		Title: "New Rust Blog Post", Link: "https://example.com/rust2",
		Published: timestamppb.Now(), FetchedAt: timestamppb.Now(),
	}
	ts.pushItem(newItem)

	// Should receive the pushed item.
	select {
	case msg, ok := <-sub.Messages:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		itemMsg, ok := msg.Msg.(*pb.ServerMessage_Item)
		if !ok {
			t.Fatalf("expected item message, got %T", msg.Msg)
		}
		if itemMsg.Item.Title != "New Rust Blog Post" {
			t.Errorf("expected 'New Rust Blog Post', got %s", itemMsg.Item.Title)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for pushed item")
	}
}

func TestClient_StreamItems_FilterChange(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := c.StreamItems(ctx, &pb.StreamFilter{
		Sort:  "newest",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("StreamItems: %v", err)
	}
	defer sub.Close()

	// Drain the initial batch.
	var gotComplete bool
	timeout := time.After(3 * time.Second)
drainForFilter:
	for {
		select {
		case msg, ok := <-sub.Messages:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			if _, ok := msg.Msg.(*pb.ServerMessage_Complete); ok {
				gotComplete = true
				break drainForFilter
			}
		case <-timeout:
			t.Fatal("timeout waiting for initial complete")
		}
	}
	if !gotComplete {
		t.Fatal("expected complete after initial batch")
	}

	// Send a filter change.
	sub.SendFilter(&pb.StreamFilter{
		SourceId: 1,
		Sort:     "newest",
		Limit:    5,
	})

	// Should receive Reset followed by items (2) and Complete.
	var gotReset bool
	var itemCount int
	gotComplete = false

	timeout = time.After(3 * time.Second)
	for itemCount < 2 || !gotComplete || !gotReset {
		select {
		case msg, ok := <-sub.Messages:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			switch msg.Msg.(type) {
			case *pb.ServerMessage_Reset_:
				gotReset = true
			case *pb.ServerMessage_Item:
				itemCount++
			case *pb.ServerMessage_Complete:
				gotComplete = true
			}
		case <-timeout:
			t.Fatalf("timeout: reset=%v items=%d complete=%v", gotReset, itemCount, gotComplete)
		}
	}

	if !gotReset {
		t.Error("expected reset message after filter change")
	}
	if itemCount != 2 {
		t.Errorf("expected 2 items after filter change, got %d", itemCount)
	}
}

func TestClient_Close(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Calling Close again should not panic.
	if err := c.Close(); err != nil {
		t.Logf("second Close returned: %v (acceptable)", err)
	}
}

func TestClient_MultipleStreamSubs(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()
	_ = ts

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	sub1, err := c.StreamItems(ctx1, &pb.StreamFilter{Sort: "newest", Limit: 10})
	if err != nil {
		t.Fatalf("StreamItems 1: %v", err)
	}
	defer sub1.Close()

	sub2, err := c.StreamItems(ctx2, &pb.StreamFilter{Sort: "newest", Limit: 10})
	if err != nil {
		t.Fatalf("StreamItems 2: %v", err)
	}
	defer sub2.Close()

	// Both should receive initial batch + complete.
	var wg sync.WaitGroup
	wg.Add(2)

	checkStream := func(sub *StreamSub, name string) {
		defer wg.Done()
		timeout := time.After(3 * time.Second)
		itemCount := 0
		for {
			select {
			case msg, ok := <-sub.Messages:
				if !ok {
					t.Errorf("%s: channel closed unexpectedly", name)
					return
				}
				if _, ok := msg.Msg.(*pb.ServerMessage_Item); ok {
					itemCount++
				}
				if _, ok := msg.Msg.(*pb.ServerMessage_Complete); ok {
					if itemCount != 2 {
						t.Errorf("%s: expected 2 items, got %d", name, itemCount)
					}
					return
				}
			case <-timeout:
				t.Errorf("%s: timeout", name)
				return
			}
		}
	}

	go checkStream(sub1, "sub1")
	go checkStream(sub2, "sub2")

	wg.Wait()
}
