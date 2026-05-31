// Package service implements the Nyttig gRPC service as thin wrappers over
// the database layer. It converts between proto types and internal db types
// and delegates all business logic to the db package.
//
// The Hub type manages active StreamItems subscriptions and pushes newly
// fetched/tagged items to connected clients in real time.
package service

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
)

// RefreshSourceFunc is called by RefreshSource RPC. The scheduler registers
// its implementation in the daemon entrypoint (task 6.2).
type RefreshSourceFunc func(ctx context.Context, sourceID int64) error

// SourceLifecycleFunc is called on AddSource or UpdateSource to notify the scheduler.
type SourceLifecycleFunc func(ctx context.Context, src db.Source)

// SourceRemoveFunc is called on RemoveSource to stop the scheduler runner.
type SourceRemoveFunc func(id int64)

// Service implements the NyttigServer gRPC interface.
type Service struct {
	pb.UnimplementedNyttigServer
	db              *sql.DB
	refreshSourceFn RefreshSourceFunc
	hub             *Hub
	onSourceAdded   SourceLifecycleFunc
	onSourceRemoved SourceRemoveFunc
	onSourceUpdated SourceLifecycleFunc
}

// New creates a Service backed by the given database.
func New(database *sql.DB) *Service {
	return &Service{
		db:  database,
		hub: NewHub(),
	}
}

// SetRefreshSourceFunc registers the scheduler callback for out-of-cycle
// refresh requests. Called by the daemon entrypoint in task 6.2.
func (s *Service) SetRefreshSourceFunc(fn RefreshSourceFunc) {
	s.refreshSourceFn = fn
}

// OnSourceAdded registers a callback invoked after a source is created.
func (s *Service) OnSourceAdded(fn SourceLifecycleFunc) {
	s.onSourceAdded = fn
}

// OnSourceRemoved registers a callback invoked before a source is deleted.
func (s *Service) OnSourceRemoved(fn SourceRemoveFunc) {
	s.onSourceRemoved = fn
}

// OnSourceUpdated registers a callback invoked when a source's enabled state changes.
func (s *Service) OnSourceUpdated(fn SourceLifecycleFunc) {
	s.onSourceUpdated = fn
}

// Hub returns the notification hub so the daemon can push new items to
// active StreamItems subscribers.
func (s *Service) Hub() *Hub {
	return s.hub
}

// ── Source management ───────────────────────────────────────────────────────

// AddSource creates a new feed source.
func (s *Service) AddSource(ctx context.Context, req *pb.AddSourceRequest) (*pb.Source, error) {
	src := &db.Source{
		Name:       req.Name,
		URL:        req.Url,
		Type:       req.Type,
		RefreshSec: int(req.RefreshSec),
		Enabled:    req.Enabled,
	}
	if src.Type == "" {
		src.Type = "rss"
	}
	if src.RefreshSec <= 0 {
		src.RefreshSec = 3600
	}

	id, err := db.InsertSource(s.db, src)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "insert source: %v", err)
	}

	created, err := db.GetSource(s.db, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get created source: %v", err)
	}

	// Notify scheduler to start fetching the new source.
	if created.Enabled && s.onSourceAdded != nil {
		s.onSourceAdded(ctx, *created)
	}

	return dbSourceToProto(created), nil
}

// RemoveSource deletes a feed source and all its related data.
func (s *Service) RemoveSource(ctx context.Context, req *pb.RemoveSourceRequest) (*emptypb.Empty, error) {
	// Notify scheduler before deleting from DB.
	if s.onSourceRemoved != nil {
		s.onSourceRemoved(req.Id)
	}

	if err := db.DeleteSource(s.db, req.Id); err != nil {
		return nil, status.Errorf(codes.Internal, "delete source: %v", err)
	}
	return &emptypb.Empty{}, nil
}

// UpdateSource modifies an existing feed source.
func (s *Service) UpdateSource(ctx context.Context, req *pb.UpdateSourceRequest) (*pb.Source, error) {
	existing, err := db.GetSource(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get source: %v", err)
	}
	if existing == nil {
		return nil, status.Errorf(codes.NotFound, "source %d not found", req.Id)
	}

	// Patch with non-zero request fields.
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.Url != "" {
		existing.URL = req.Url
	}
	if req.Type != "" {
		existing.Type = req.Type
	}
	if req.RefreshSec > 0 {
		existing.RefreshSec = int(req.RefreshSec)
	}
	// Enabled: only update if the caller explicitly sets it.
	// Because proto3 defaults bool to false, we cannot distinguish "not set"
	// from "set to false" without a wrapper (e.g., optional).
	// Leave enabled unchanged unless the request explicitly intends a toggle.

	if err := db.UpdateSource(s.db, existing); err != nil {
		return nil, status.Errorf(codes.Internal, "update source: %v", err)
	}

	// Notify scheduler of enabled/disabled change.
	if s.onSourceUpdated != nil {
		s.onSourceUpdated(ctx, *existing)
	}

	updated, err := db.GetSource(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get updated source: %v", err)
	}
	return dbSourceToProto(updated), nil
}

// ListSources returns all configured feed sources.
func (s *Service) ListSources(ctx context.Context, _ *emptypb.Empty) (*pb.ListSourcesResponse, error) {
	sources, err := db.ListSources(s.db)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list sources: %v", err)
	}
	resp := &pb.ListSourcesResponse{Sources: make([]*pb.Source, len(sources))}
	for i, src := range sources {
		resp.Sources[i] = dbSourceToProto(src)
	}
	return resp, nil
}

// RefreshSource triggers an immediate out-of-cycle fetch for the given source
// (or all sources if source_id is 0).
func (s *Service) RefreshSource(ctx context.Context, req *pb.RefreshSourceRequest) (*emptypb.Empty, error) {
	if s.refreshSourceFn == nil {
		return nil, status.Error(codes.Unavailable, "refresh source handler not configured")
	}

	if req.SourceId == 0 {
		// Refresh all enabled sources.
		sources, err := db.ListEnabledSources(s.db)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "list enabled sources: %v", err)
		}
		for _, src := range sources {
			if err := s.refreshSourceFn(ctx, src.ID); err != nil {
				// Log and continue; don't let one source block others.
				slog.Warn("refresh source failed", "source_id", src.ID, "error", err)
				continue
			}
		}
	} else {
		if err := s.refreshSourceFn(ctx, req.SourceId); err != nil {
			return nil, status.Errorf(codes.Internal, "refresh source %d: %v", req.SourceId, err)
		}
	}

	return &emptypb.Empty{}, nil
}

// ── Tag management ─────────────────────────────────────────────────────────

// AddTag creates a new tag.
func (s *Service) AddTag(ctx context.Context, req *pb.AddTagRequest) (*pb.Tag, error) {
	var color *string
	if req.Color != "" {
		color = &req.Color
	}

	id, err := db.InsertTag(s.db, req.Name, color)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "insert tag: %v", err)
	}

	tag, err := db.GetTag(s.db, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get created tag: %v", err)
	}
	return dbTagToProto(tag), nil
}

// RemoveTag deletes a tag and all its associated rules and item associations.
func (s *Service) RemoveTag(ctx context.Context, req *pb.RemoveTagRequest) (*emptypb.Empty, error) {
	if err := db.DeleteTag(s.db, req.Id); err != nil {
		return nil, status.Errorf(codes.Internal, "delete tag: %v", err)
	}
	return &emptypb.Empty{}, nil
}

// ListTags returns all defined tags.
func (s *Service) ListTags(ctx context.Context, _ *emptypb.Empty) (*pb.ListTagsResponse, error) {
	tags, err := db.ListTags(s.db)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list tags: %v", err)
	}
	resp := &pb.ListTagsResponse{Tags: make([]*pb.Tag, len(tags))}
	for i, t := range tags {
		resp.Tags[i] = dbTagToProto(t)
	}
	return resp, nil
}

// ── Tag rules ──────────────────────────────────────────────────────────────

// AddTagRule creates a new tag rule.
func (s *Service) AddTagRule(ctx context.Context, req *pb.AddTagRuleRequest) (*pb.TagRule, error) {
	rule := &db.TagRule{
		TagID:    req.TagId,
		Field:    req.Field,
		Pattern:  req.Pattern,
		Priority: int(req.Priority),
	}
	if req.SourceId != 0 {
		rule.SourceID = &req.SourceId
	}
	if rule.Field == "" {
		rule.Field = "both"
	}

	id, err := db.InsertTagRule(s.db, rule)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "insert tag rule: %v", err)
	}

	created, err := db.GetTagRule(s.db, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get created tag rule: %v", err)
	}
	return dbTagRuleToProto(created), nil
}

// RemoveTagRule deletes a tag rule by ID.
func (s *Service) RemoveTagRule(ctx context.Context, req *pb.RemoveTagRuleRequest) (*emptypb.Empty, error) {
	if err := db.DeleteTagRule(s.db, req.Id); err != nil {
		return nil, status.Errorf(codes.Internal, "delete tag rule: %v", err)
	}
	return &emptypb.Empty{}, nil
}

// ListTagRules returns all tag rules.
func (s *Service) ListTagRules(ctx context.Context, _ *emptypb.Empty) (*pb.ListTagRulesResponse, error) {
	rules, err := db.ListTagRules(s.db, nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list tag rules: %v", err)
	}
	resp := &pb.ListTagRulesResponse{Rules: make([]*pb.TagRule, len(rules))}
	for i, r := range rules {
		resp.Rules[i] = dbTagRuleToProto(r)
	}
	return resp, nil
}

// ── Search ─────────────────────────────────────────────────────────────────

// Search performs an FTS5 full-text search across items.
func (s *Service) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	items, total, err := db.SearchItems(s.db, req.Query, req.SourceId, req.TagId, int(req.Limit), int(req.Offset))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "search: %v", err)
	}

	resp := &pb.SearchResponse{
		Items: make([]*pb.Item, len(items)),
		Total: int32(total),
	}
	for i, item := range items {
		resp.Items[i] = ItemToProto(item)
	}
	return resp, nil
}

// ── View tracking ──────────────────────────────────────────────────────────

// MarkViewed records the given item IDs as viewed (idempotent).
func (s *Service) MarkViewed(ctx context.Context, req *pb.MarkViewedRequest) (*emptypb.Empty, error) {
	if err := db.MarkViewed(s.db, req.ItemIds); err != nil {
		return nil, status.Errorf(codes.Internal, "mark viewed: %v", err)
	}
	return &emptypb.Empty{}, nil
}

// ── StreamItems (bidirectional streaming) ──────────────────────────────────

// sub represents a single StreamItems client connection.
type sub struct {
	sendCh chan *pb.ServerMessage
	ctx    context.Context
}

// Hub manages active StreamItems subscribers and broadcasts items to them.
// The daemon calls Hub.Push when new items are fetched and tagged.
type Hub struct {
	mu   sync.Mutex
	subs map[*sub]struct{}
}

// NewHub creates a Hub.
func NewHub() *Hub {
	return &Hub{
		subs: make(map[*sub]struct{}),
	}
}

// subscribe registers a new subscriber and returns its send channel.
// The caller is responsible for calling unsubscribe when the stream ends.
func (h *Hub) subscribe(ctx context.Context) *sub {
	s := &sub{
		sendCh: make(chan *pb.ServerMessage, 64),
		ctx:    ctx,
	}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// unsubscribe removes a subscriber.
func (h *Hub) unsubscribe(s *sub) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// Push broadcasts an item to all connected subscribers. Non-blocking:
// if a subscriber's buffer is full, the item is dropped for that subscriber.
func (h *Hub) Push(item *pb.Item) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.sendCh <- &pb.ServerMessage{Msg: &pb.ServerMessage_Item{Item: item}}:
		default:
			// Buffer full; drop for this slow subscriber.
		}
	}
}

// StreamItems implements the bidirectional streaming RPC.
//
// Protocol:
//  1. Client sends a StreamFilter.
//  2. Server queries the DB, sends matching items one by one, then sends
//     a Complete message.
//  3. Whenever the scheduler fetches new items, they are pushed immediately.
//  4. If the client sends another StreamFilter, the server resets by sending
//     Reset, re-sending matching items, then Complete.
func (s *Service) StreamItems(stream pb.Nyttig_StreamItemsServer) error {
	ctx := stream.Context()
	sub := s.hub.subscribe(ctx)
	defer s.hub.unsubscribe(sub)

	// Channel to receive ClientMessages from the stream.
	clientMsgCh := make(chan *pb.ClientMessage, 1)

	// Goroutine: read ClientMessages from the stream.
	recvErrCh := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				recvErrCh <- err
				return
			}
			select {
			case clientMsgCh <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for the first filter message.
	var filter *pb.StreamFilter
	select {
	case msg := <-clientMsgCh:
		if filterMsg := msg.GetFilter(); filterMsg != nil {
			filter = filterMsg
		}
	case err := <-recvErrCh:
		if err == io.EOF {
			return nil
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

	if filter == nil {
		return status.Error(codes.InvalidArgument, "first message must be a filter")
	}

	// Send initial batch for the current filter.
	if err := s.sendFilteredItems(stream, filter); err != nil {
		return err
	}

	// Main loop: handle filter changes and new-item pushes.
	for {
		select {
		case msg, ok := <-clientMsgCh:
			if !ok {
				return nil
			}
			if newFilter := msg.GetFilter(); newFilter != nil {
				filter = newFilter
				// Signal reset.
				if err := stream.Send(&pb.ServerMessage{Msg: &pb.ServerMessage_Reset_{Reset_: &pb.Reset{}}}); err != nil {
					return err
				}
				// Re-send matching items.
				if err := s.sendFilteredItems(stream, filter); err != nil {
					return err
				}
			}

		case itemMsg, ok := <-sub.sendCh:
			if !ok {
				return nil
			}
			// Push new item if it matches the current filter.
			if item := itemMsg.GetItem(); item != nil && s.itemMatchesFilter(item, filter) {
				if err := stream.Send(itemMsg); err != nil {
					return err
				}
			}

		case err := <-recvErrCh:
			if err == io.EOF {
				return nil
			}
			return err

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// sendFilteredItems queries the database with the given filter and sends
// matching items followed by a Complete message.
func (s *Service) sendFilteredItems(stream pb.Nyttig_StreamItemsServer, filter *pb.StreamFilter) error {
	limit := int(filter.Limit)
	if limit <= 0 {
		limit = 200
	}

	sort := filter.Sort
	if sort == "" {
		sort = "newest"
	}

	dbFilter := db.ItemFilter{
		SourceID:     filter.SourceId,
		TagID:        filter.TagId,
		Search:       filter.Search,
		Sort:         sort,
		Limit:        limit,
		UnviewedOnly: filter.UnviewedOnly,
	}

	items, _, err := db.ListItems(s.db, dbFilter)
	if err != nil {
		slog.Error("stream: list items", "error", err)
		return status.Errorf(codes.Internal, "list items: %v", err)
	}

	for _, item := range items {
		if err := stream.Send(&pb.ServerMessage{
			Msg: &pb.ServerMessage_Item{Item: ItemToProto(item)},
		}); err != nil {
			return err
		}
	}

	return stream.Send(&pb.ServerMessage{
		Msg: &pb.ServerMessage_Complete{Complete: &pb.Complete{}},
	})
}

// itemMatchesFilter returns true if the item passes the current filter.
func (s *Service) itemMatchesFilter(item *pb.Item, filter *pb.StreamFilter) bool {
	if filter == nil {
		return true
	}
	if filter.SourceId != 0 && item.SourceId != filter.SourceId {
		return false
	}
	if filter.TagId != 0 {
		hasTag := false
		for _, t := range item.Tags {
			if t.Id == filter.TagId {
				hasTag = true
				break
			}
		}
		if !hasTag {
			return false
		}
	}
	if filter.UnviewedOnly && item.Viewed {
		return false
	}
	// Note: FTS5 search filtering is not applied here because new items
	// pushed mid-stream are typically unfiltered; the client can filter
	// them on its side if needed.
	return true
}

// ── Conversion helpers ─────────────────────────────────────────────────────

func dbSourceToProto(src *db.Source) *pb.Source {
	p := &pb.Source{
		Id:         src.ID,
		Name:       src.Name,
		Url:        src.URL,
		Type:       src.Type,
		RefreshSec: int32(src.RefreshSec),
		Enabled:    src.Enabled,
		CreatedAt:  timestamppb.New(src.CreatedAt),
	}
	if src.LastFetch != nil {
		p.LastFetch = timestamppb.New(*src.LastFetch)
	}
	if src.FetchError != nil {
		p.FetchError = *src.FetchError
	}
	return p
}

func dbTagToProto(t *db.Tag) *pb.Tag {
	p := &pb.Tag{
		Id:   t.ID,
		Name: t.Name,
	}
	if t.Color != nil {
		p.Color = *t.Color
	}
	return p
}

func dbTagRuleToProto(r *db.TagRule) *pb.TagRule {
	p := &pb.TagRule{
		Id:       r.ID,
		TagId:    r.TagID,
		TagName:  r.TagName,
		Field:    r.Field,
		Pattern:  r.Pattern,
		Priority: int32(r.Priority),
	}
	if r.SourceID != nil {
		p.SourceId = *r.SourceID
	}
	return p
}

// ItemToProto converts a db.Item (with tags loaded) to a protobuf Item.
func ItemToProto(item *db.Item) *pb.Item {
	p := &pb.Item{
		Id:         item.ID,
		SourceId:   item.SourceID,
		SourceName: item.SourceName,
		Guid:       item.GUID,
		Link:       item.Link,
		Title:      item.Title,
		FetchedAt:  timestamppb.New(item.FetchedAt),
		Tags:       make([]*pb.Tag, len(item.Tags)),
		Viewed:     item.Viewed,
	}
	if item.Description != nil {
		p.Description = *item.Description
	}
	if item.Author != nil {
		p.Author = *item.Author
	}
	if item.Published != nil {
		p.Published = timestamppb.New(*item.Published)
	}
	for i, t := range item.Tags {
		p.Tags[i] = dbTagToProto(t)
	}
	return p
}
