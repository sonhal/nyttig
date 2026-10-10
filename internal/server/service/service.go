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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
	"github.com/sonhal/nyttig/internal/server/fetcher"
)

// RefreshSourceFunc is called by RefreshSource RPC. The scheduler registers
// its implementation in the daemon entrypoint (task 6.2).
type RefreshSourceFunc func(ctx context.Context, sourceID int64) error

// SourceLifecycleFunc is called on AddSource or UpdateSource to notify the scheduler.
type SourceLifecycleFunc func(ctx context.Context, src db.Source)

// SourceRemoveFunc is called on RemoveSource to stop the scheduler runner.
type SourceRemoveFunc func(id int64)

// ProfileResolver looks up a Bluesky account by handle or DID. The fetcher's
// BlueskyResolver implements it; tests use a fake.
type ProfileResolver interface {
	ResolveProfile(ctx context.Context, actor string) (*fetcher.BlueskyProfile, error)
}

// Service implements the NyttigServer gRPC interface.
type Service struct {
	pb.UnimplementedNyttigServer
	db              *sql.DB
	refreshSourceFn RefreshSourceFunc
	hub             *Hub
	onSourceAdded   SourceLifecycleFunc
	onSourceRemoved SourceRemoveFunc
	onSourceUpdated SourceLifecycleFunc
	profiles        ProfileResolver

	// tagGraph is a snapshot of the tag tree for itemMatchesFilter, rebuilt
	// after every tag change (ReloadTagGraph).
	tagGraph atomic.Pointer[tagGraph]
}

// New creates a Service backed by the given database.
func New(database *sql.DB) *Service {
	s := &Service{
		db:  database,
		hub: NewHub(),
	}
	s.tagGraph.Store(&tagGraph{})
	if err := s.ReloadTagGraph(); err != nil {
		slog.Warn("load tag tree", "error", err)
	}
	return s
}

// ReloadTagGraph rebuilds the tag tree snapshot that stream filters use. The
// service calls it after every tag change; the daemon calls it after seeding
// tags from the config, which writes to the database directly.
func (s *Service) ReloadTagGraph() error {
	edges, err := db.ListTagEdges(s.db)
	if err != nil {
		return err
	}
	s.tagGraph.Store(newTagGraph(edges))
	return nil
}

// reloadTagGraph is ReloadTagGraph for the RPC handlers, whose own write has
// already succeeded: a failure is logged, not returned.
func (s *Service) reloadTagGraph() {
	if err := s.ReloadTagGraph(); err != nil {
		slog.Error("reload tag tree", "error", err)
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

// OnSourceUpdated registers a callback invoked after an update that changes
// how a source is fetched: its enabled state, url, type or refresh interval.
func (s *Service) OnSourceUpdated(fn SourceLifecycleFunc) {
	s.onSourceUpdated = fn
}

// SetProfileResolver registers the Bluesky account lookup AddSource and
// UpdateSource use to turn a handle into a DID. Without one, adding a Bluesky
// source fails with Unavailable.
func (s *Service) SetProfileResolver(r ProfileResolver) {
	s.profiles = r
}

// resolveBluesky looks up the account named by input (a handle, DID or
// profile URL, already syntax-checked). Bluesky rejecting the account, such
// as an unknown handle, is InvalidArgument; failing to reach Bluesky is
// Unavailable.
func (s *Service) resolveBluesky(ctx context.Context, input string) (*fetcher.BlueskyProfile, error) {
	actor, err := fetcher.ParseBlueskyActor(input)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if s.profiles == nil {
		return nil, status.Error(codes.Unavailable, "bluesky lookup is not configured")
	}
	profile, err := s.profiles.ResolveProfile(ctx, actor)
	if err != nil {
		var xerr *fetcher.XRPCError
		// Bluesky answered that it has no such account (or rejected the
		// request); a rate limit or server error is not the user's fault.
		if errors.As(err, &xerr) && xerr.Status >= 400 && xerr.Status < 500 && xerr.Status != 429 {
			return nil, status.Errorf(codes.InvalidArgument, "bluesky: %v", xerr)
		}
		return nil, status.Errorf(codes.Unavailable, "bluesky lookup failed: %v", err)
	}
	return profile, nil
}

// blueskyDefaultName names a source after the account: its display name, or
// @handle.
func blueskyDefaultName(p *fetcher.BlueskyProfile) string {
	name := p.DisplayName
	if name == "" {
		name = "@" + p.Handle
	}
	if r := []rune(name); len(r) > maxNameLen {
		name = string(r[:maxNameLen])
	}
	return name
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
	if src.RefreshSec == 0 {
		src.RefreshSec = defaultRefreshSec
	}
	if err := firstErr(
		validateSourceName(src.Type, src.Name),
		validateFeedType(src.Type),
		validateSourceURL(src.Type, src.URL),
		validateRefreshSec(int32(src.RefreshSec)),
		validateColor(req.Color),
		validateAbbreviation(req.Abbreviation),
	); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if src.Type == fetcher.TypeKEV {
		// Store the URL that is read, so the source list shows it and
		// sources.url's UNIQUE sees it.
		if strings.TrimSpace(src.URL) == "" {
			src.URL = fetcher.KEVDefaultURL
		}
		if strings.TrimSpace(src.Name) == "" {
			src.Name = fetcher.KEVDefaultName
		}
	}
	if src.Type == fetcher.TypeBluesky {
		// Store the account by DID: a handle can change hands.
		profile, err := s.resolveBluesky(ctx, src.URL)
		if err != nil {
			return nil, err
		}
		src.URL = fetcher.BlueskyProfileURL(profile.DID)
		if strings.TrimSpace(src.Name) == "" {
			src.Name = blueskyDefaultName(profile)
		}
		if err := validateName("name", src.Name, maxNameLen); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	if req.Color != "" {
		src.Color = &req.Color
	}
	if req.Abbreviation != "" {
		src.Abbreviation = &req.Abbreviation
	}

	id, err := db.InsertSource(s.db, src)
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "a source with url %q already exists", src.URL)
	}
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

// RemoveSource deletes a feed source and all its related data. An unknown
// ID is NotFound.
func (s *Service) RemoveSource(ctx context.Context, req *pb.RemoveSourceRequest) (*emptypb.Empty, error) {
	existing, err := db.GetSource(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get source: %v", err)
	}
	if existing == nil {
		return nil, status.Errorf(codes.NotFound, "source %d not found", req.Id)
	}

	// Notify scheduler before deleting from DB.
	if s.onSourceRemoved != nil {
		s.onSourceRemoved(req.Id)
	}

	deleted, err := db.DeleteSource(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete source: %v", err)
	}
	if !deleted { // removed by another call since the lookup
		return nil, status.Errorf(codes.NotFound, "source %d not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}

// UpdateSource patches an existing feed source. Only fields set in the
// request are changed; for color and abbreviation an empty string clears the
// value. The scheduler is notified when a change affects fetching (enabled,
// url, type or refresh interval), so edits take effect without a daemon restart.
func (s *Service) UpdateSource(ctx context.Context, req *pb.UpdateSourceRequest) (*pb.Source, error) {
	existing, err := db.GetSource(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get source: %v", err)
	}
	if existing == nil {
		return nil, status.Errorf(codes.NotFound, "source %d not found", req.Id)
	}
	before := *existing

	var errs []error
	if req.Name != nil {
		errs = append(errs, validateName("name", *req.Name, maxNameLen))
		existing.Name = *req.Name
	}
	if req.Url != nil {
		existing.URL = *req.Url
	}
	if req.Type != nil {
		existing.Type = *req.Type
	}
	if req.Url != nil || req.Type != nil {
		// The url's rules depend on the type, so check the merged result.
		errs = append(errs, validateFeedType(existing.Type), validateSourceURL(existing.Type, existing.URL))
	}
	if req.RefreshSec != nil {
		errs = append(errs, validateRefreshSec(*req.RefreshSec))
		existing.RefreshSec = int(*req.RefreshSec)
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.Color != nil {
		errs = append(errs, validateColor(*req.Color))
		existing.Color = optionalString(*req.Color)
	}
	if req.Abbreviation != nil {
		errs = append(errs, validateAbbreviation(*req.Abbreviation))
		existing.Abbreviation = optionalString(*req.Abbreviation)
	}
	if err := firstErr(errs...); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if existing.Type == fetcher.TypeKEV && strings.TrimSpace(existing.URL) == "" {
		existing.URL = fetcher.KEVDefaultURL
	}
	if existing.Type == fetcher.TypeBluesky && (existing.Type != before.Type || existing.URL != before.URL) {
		profile, err := s.resolveBluesky(ctx, existing.URL)
		if err != nil {
			return nil, err
		}
		existing.URL = fetcher.BlueskyProfileURL(profile.DID)
	}

	err = db.UpdateSource(s.db, existing)
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "a source with url %q already exists", existing.URL)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "update source: %v", err)
	}

	fetchChanged := before.Enabled != existing.Enabled ||
		before.URL != existing.URL ||
		before.Type != existing.Type ||
		before.RefreshSec != existing.RefreshSec
	if fetchChanged && s.onSourceUpdated != nil {
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
	if err := firstErr(
		validateName("name", req.Name, maxTagNameLen),
		validateColor(req.Color),
		validateParentIDs(req.ParentIds),
	); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	id, err := db.InsertTagWithParents(s.db, req.Name, optionalString(req.Color), req.ParentIds)
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "tag %q already exists", req.Name)
	}
	if err != nil {
		return nil, tagTreeError(err, "insert tag")
	}
	s.reloadTagGraph()

	tag, err := db.GetTag(s.db, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get created tag: %v", err)
	}
	tag.ParentIDs = req.ParentIds
	return dbTagToProto(tag), nil
}

// UpdateTag renames, recolors and/or re-parents a tag in place. Unlike removing and
// re-adding a tag, this keeps its rules and item assignments.
func (s *Service) UpdateTag(ctx context.Context, req *pb.UpdateTagRequest) (*pb.Tag, error) {
	tag, err := db.GetTag(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get tag: %v", err)
	}
	if tag == nil {
		return nil, status.Errorf(codes.NotFound, "tag %d not found", req.Id)
	}

	var errs []error
	if req.Name != nil {
		errs = append(errs, validateName("name", *req.Name, maxTagNameLen))
		tag.Name = *req.Name
	}
	if req.Color != nil {
		errs = append(errs, validateColor(*req.Color))
		tag.Color = optionalString(*req.Color)
	}
	var parents *[]int64
	if req.Parents != nil {
		errs = append(errs, validateParentIDs(req.Parents.Ids))
		ids := req.Parents.Ids
		if ids == nil {
			ids = []int64{}
		}
		parents = &ids
	}
	if err := firstErr(errs...); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	err = db.UpdateTagAndParents(s.db, tag, parents)
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "tag %q already exists", tag.Name)
	}
	if err != nil {
		return nil, tagTreeError(err, "update tag")
	}
	s.reloadTagGraph()

	updated, err := db.GetTag(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get updated tag: %v", err)
	}
	if err := s.fillParents(updated); err != nil {
		return nil, status.Errorf(codes.Internal, "get tag parents: %v", err)
	}
	return dbTagToProto(updated), nil
}

// RemoveTag deletes a tag and all its associated rules, item associations and
// parent edges. Its children are kept (a child with no other parent becomes
// top-level). An unknown ID is NotFound.
func (s *Service) RemoveTag(ctx context.Context, req *pb.RemoveTagRequest) (*emptypb.Empty, error) {
	deleted, err := db.DeleteTag(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete tag: %v", err)
	}
	if !deleted {
		return nil, status.Errorf(codes.NotFound, "tag %d not found", req.Id)
	}
	s.reloadTagGraph()
	return &emptypb.Empty{}, nil
}

// fillParents sets t.ParentIDs, which GetTag leaves empty.
func (s *Service) fillParents(t *db.Tag) error {
	edges, err := db.ListTagEdges(s.db)
	if err != nil {
		return err
	}
	t.ParentIDs = nil
	for _, e := range edges {
		if e.ChildID == t.ID {
			t.ParentIDs = append(t.ParentIDs, e.ParentID)
		}
	}
	return nil
}

// tagTreeError maps the db layer's tag-tree errors to gRPC codes: a cycle is
// the client's mistake, an unknown parent is NotFound.
func tagTreeError(err error, what string) error {
	var cyc *db.ErrTagCycle
	var nf *db.ErrTagNotFound
	switch {
	case errors.As(err, &cyc):
		return status.Error(codes.InvalidArgument, cyc.Error())
	case errors.As(err, &nf):
		return status.Error(codes.NotFound, nf.Error())
	default:
		return status.Errorf(codes.Internal, "%s: %v", what, err)
	}
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

// ── Saved views ────────────────────────────────────────────────────────────

// AddSavedView creates a view at the end of the order.
func (s *Service) AddSavedView(ctx context.Context, req *pb.AddSavedViewRequest) (*pb.SavedView, error) {
	if err := firstErr(
		validateName("name", req.Name, maxViewNameLen),
		validateViewFilter(req.Filter),
	); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.checkViewRefs(req.Filter); err != nil {
		return nil, err
	}
	n, err := db.CountSavedViews(s.db)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "count saved views: %v", err)
	}
	if n >= maxSavedViews {
		return nil, status.Errorf(codes.FailedPrecondition, "at most %d saved views are allowed", maxSavedViews)
	}

	v := &db.SavedView{Name: req.Name, Favorite: req.Favorite}
	applyViewFilter(v, req.Filter)
	id, err := db.InsertSavedView(s.db, v)
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "view %q already exists", req.Name)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "insert saved view: %v", err)
	}
	created, err := db.GetSavedView(s.db, id)
	if err != nil || created == nil {
		return nil, status.Errorf(codes.Internal, "get created saved view: %v", err)
	}
	return dbSavedViewToProto(created), nil
}

// UpdateSavedView renames a view, replaces its filter and/or toggles its
// favorite flag. Unset fields are unchanged.
func (s *Service) UpdateSavedView(ctx context.Context, req *pb.UpdateSavedViewRequest) (*pb.SavedView, error) {
	v, err := db.GetSavedView(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get saved view: %v", err)
	}
	if v == nil {
		return nil, status.Errorf(codes.NotFound, "view %d not found", req.Id)
	}

	var errs []error
	if req.Name != nil {
		errs = append(errs, validateName("name", *req.Name, maxViewNameLen))
		v.Name = *req.Name
	}
	if req.Filter != nil {
		errs = append(errs, validateViewFilter(req.Filter))
	}
	if err := firstErr(errs...); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if req.Filter != nil {
		if err := s.checkViewRefs(req.Filter); err != nil {
			return nil, err
		}
		applyViewFilter(v, req.Filter)
	}
	if req.Favorite != nil {
		v.Favorite = *req.Favorite
	}

	_, err = db.UpdateSavedView(s.db, v)
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "view %q already exists", v.Name)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "update saved view: %v", err)
	}
	updated, err := db.GetSavedView(s.db, req.Id)
	if err != nil || updated == nil {
		return nil, status.Errorf(codes.Internal, "get updated saved view: %v", err)
	}
	return dbSavedViewToProto(updated), nil
}

// RemoveSavedView deletes a view. An unknown ID is NotFound.
func (s *Service) RemoveSavedView(ctx context.Context, req *pb.RemoveSavedViewRequest) (*emptypb.Empty, error) {
	deleted, err := db.DeleteSavedView(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete saved view: %v", err)
	}
	if !deleted {
		return nil, status.Errorf(codes.NotFound, "view %d not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}

// ListSavedViews returns every view in display order.
func (s *Service) ListSavedViews(ctx context.Context, _ *emptypb.Empty) (*pb.ListSavedViewsResponse, error) {
	return s.savedViewsResponse()
}

// ReorderSavedViews sets the display order. ids must list every view exactly
// once.
func (s *Service) ReorderSavedViews(ctx context.Context, req *pb.ReorderSavedViewsRequest) (*pb.ListSavedViewsResponse, error) {
	err := db.ReorderSavedViews(s.db, req.Ids)
	if errors.Is(err, db.ErrViewOrder) {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reorder saved views: %v", err)
	}
	return s.savedViewsResponse()
}

func (s *Service) savedViewsResponse() (*pb.ListSavedViewsResponse, error) {
	views, err := db.ListSavedViews(s.db)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list saved views: %v", err)
	}
	resp := &pb.ListSavedViewsResponse{Views: make([]*pb.SavedView, len(views))}
	for i, v := range views {
		resp.Views[i] = dbSavedViewToProto(v)
	}
	return resp, nil
}

// checkViewRefs reports NotFound for a source or tag the filter names that
// does not exist.
func (s *Service) checkViewRefs(f *pb.ViewFilter) error {
	if f == nil {
		return nil
	}
	if f.SourceId != 0 {
		src, err := db.GetSource(s.db, f.SourceId)
		if err != nil {
			return status.Errorf(codes.Internal, "get source: %v", err)
		}
		if src == nil {
			return status.Errorf(codes.NotFound, "source %d not found", f.SourceId)
		}
	}
	if f.TagId != 0 {
		tag, err := db.GetTag(s.db, f.TagId)
		if err != nil {
			return status.Errorf(codes.Internal, "get tag: %v", err)
		}
		if tag == nil {
			return status.Errorf(codes.NotFound, "tag %d not found", f.TagId)
		}
	}
	for _, id := range []int64{f.AssessorId, f.UnassessedBy} {
		if id == 0 {
			continue
		}
		if err := s.checkAssessor(id); err != nil {
			return err
		}
	}
	return nil
}

// applyViewFilter copies a (validated) proto filter onto v; nil clears it.
func applyViewFilter(v *db.SavedView, f *pb.ViewFilter) {
	v.Search, v.SourceID, v.TagID, v.Sort, v.UnviewedOnly, v.Since = "", nil, nil, "newest", false, ""
	v.AssessorID, v.MinScore, v.UnassessedBy = nil, nil, nil
	if f == nil {
		return
	}
	v.Search = f.Search
	if f.SourceId != 0 {
		id := f.SourceId
		v.SourceID = &id
	}
	if f.TagId != 0 {
		id := f.TagId
		v.TagID = &id
	}
	if f.Sort != "" {
		v.Sort = f.Sort
	}
	v.UnviewedOnly = f.UnviewedOnly
	v.Since = f.Since
	if f.AssessorId != 0 {
		id := f.AssessorId
		v.AssessorID = &id
	}
	if f.MinScore != nil {
		score := *f.MinScore
		v.MinScore = &score
	}
	if f.UnassessedBy != 0 {
		id := f.UnassessedBy
		v.UnassessedBy = &id
	}
}

func dbSavedViewToProto(v *db.SavedView) *pb.SavedView {
	f := &pb.ViewFilter{Search: v.Search, Sort: v.Sort, UnviewedOnly: v.UnviewedOnly, Since: v.Since}
	if v.SourceID != nil {
		f.SourceId = *v.SourceID
	}
	if v.TagID != nil {
		f.TagId = *v.TagID
	}
	if v.AssessorID != nil {
		f.AssessorId = *v.AssessorID
	}
	if v.MinScore != nil {
		score := *v.MinScore
		f.MinScore = &score
	}
	if v.UnassessedBy != nil {
		f.UnassessedBy = *v.UnassessedBy
	}
	return &pb.SavedView{Id: v.ID, Name: v.Name, Filter: f, Favorite: v.Favorite, Position: int32(v.Position)}
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
	if req.TagId == 0 {
		return nil, status.Error(codes.InvalidArgument, "tag_id is required")
	}
	// Reject bad patterns up front; the tagger would otherwise skip the rule
	// silently on every fetch.
	if err := validateField(rule.Field); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if _, err := compilePattern(rule.Pattern); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
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

// RemoveTagRule deletes a tag rule by ID. An unknown ID is NotFound.
func (s *Service) RemoveTagRule(ctx context.Context, req *pb.RemoveTagRuleRequest) (*emptypb.Empty, error) {
	deleted, err := db.DeleteTagRule(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete tag rule: %v", err)
	}
	if !deleted {
		return nil, status.Errorf(codes.NotFound, "tag rule %d not found", req.Id)
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

// testTagRuleScan is how many of the most recent items TestTagRule checks.
const testTagRuleScan = 500

// TestTagRule dry-runs a rule against recent items without saving it, so a
// client can preview what a pattern would tag. It matches exactly like the
// tagger (Go RE2 on the raw title/description), which a client could not
// reproduce with its own regex engine.
func (s *Service) TestTagRule(ctx context.Context, req *pb.TestTagRuleRequest) (*pb.TestTagRuleResponse, error) {
	field := req.Field
	if field == "" {
		field = "both"
	}
	if err := validateField(field); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	re, err := compilePattern(req.Pattern)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	items, _, err := db.ListItems(s.db, db.ItemFilter{
		SourceID: req.SourceId,
		Sort:     "newest",
		Limit:    testTagRuleScan,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list items: %v", err)
	}

	resp := &pb.TestTagRuleResponse{Scanned: int32(len(items))}
	for _, item := range items {
		desc := ""
		if item.Description != nil {
			desc = *item.Description
		}
		var match bool
		switch field {
		case "title":
			match = re.MatchString(item.Title)
		case "description":
			match = re.MatchString(desc)
		default:
			match = re.MatchString(item.Title) || re.MatchString(desc)
		}
		if match {
			resp.Items = append(resp.Items, ItemToProto(item))
			if len(resp.Items) == limit {
				break
			}
		}
	}
	return resp, nil
}

// ── Search ─────────────────────────────────────────────────────────────────

// Search performs an FTS5 full-text search across items. With an empty query
// it lists items matching the other filters, which clients use for paging
// beyond what StreamItems sends.
func (s *Service) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	if err := firstErr(
		validateSort(req.Sort),
		validateAssessmentFilter(req.AssessorId, req.MinScore, req.UnassessedBy, req.Sort),
	); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	after, err := afterTime(req.After)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 100
	}
	items, total, err := db.ListItems(s.db, db.ItemFilter{
		SourceID:     req.SourceId,
		TagID:        req.TagId,
		TagExact:     req.TagExact,
		Search:       req.Query,
		Sort:         req.Sort,
		Limit:        limit,
		Offset:       int(req.Offset),
		UnviewedOnly: req.UnviewedOnly,
		After:        after,
		AssessorID:   req.AssessorId,
		MinScore:     req.MinScore,
		UnassessedBy: req.UnassessedBy,
	})
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

	closeOnce sync.Once
	done      chan struct{} // closed by Close
}

// NewHub creates a Hub.
func NewHub() *Hub {
	return &Hub{
		subs: make(map[*sub]struct{}),
		done: make(chan struct{}),
	}
}

// Close ends every active StreamItems call, and any that starts later, with
// codes.Unavailable. The daemon calls it before grpc.Server.GracefulStop:
// GracefulStop waits for running RPCs, and a stream only ends when its
// client leaves, so without Close a connected client holds up every
// shutdown until the timeout. Clients reconnect on Unavailable. Close is
// idempotent, and Push keeps working (it just has no one to deliver to).
func (h *Hub) Close() {
	h.closeOnce.Do(func() { close(h.done) })
}

// errHubClosed is what StreamItems returns once the hub is closed.
func errHubClosed() error {
	return status.Error(codes.Unavailable, "server shutting down")
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

// PushUpdate broadcasts an item whose assessments changed, with all of them.
// Like Push it never blocks and drops the message for a slow subscriber.
// Each subscriber's StreamItems decides whether the item matches its filter.
func (h *Hub) PushUpdate(item *pb.Item) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.sendCh <- &pb.ServerMessage{Msg: &pb.ServerMessage_ItemUpdate{ItemUpdate: item}}:
		default:
			// Buffer full; this subscriber stays stale until its next reset.
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
	hubDone := s.hub.done

	// A stream that starts after Close ends at once. Checked up front
	// because the selects below pick at random among ready cases.
	select {
	case <-hubDone:
		return errHubClosed()
	default:
	}

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
	case <-hubDone:
		return errHubClosed()
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
			// An updated item is always sent: the client may be showing it
			// even if it no longer matches (it is never removed live). The
			// flag tells it whether to insert an item it does not show.
			if item := itemMsg.GetItemUpdate(); item != nil {
				out := &pb.ServerMessage{
					Msg:           itemMsg.Msg,
					UpdateMatches: s.itemMatchesFilter(item, filter),
				}
				if err := stream.Send(out); err != nil {
					return err
				}
			}

		case err := <-recvErrCh:
			if err == io.EOF {
				return nil
			}
			return err

		case <-hubDone:
			return errHubClosed()

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// sendFilteredItems queries the database with the given filter and sends
// matching items followed by a Complete message.
func (s *Service) sendFilteredItems(stream pb.Nyttig_StreamItemsServer, filter *pb.StreamFilter) error {
	after, err := afterTime(filter.After)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	limit := int(filter.Limit)
	if limit <= 0 {
		limit = 200
	}

	if err := firstErr(
		validateSort(filter.Sort),
		validateAssessmentFilter(filter.AssessorId, filter.MinScore, filter.UnassessedBy, filter.Sort),
	); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
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
		After:        after,
		AssessorID:   filter.AssessorId,
		MinScore:     filter.MinScore,
		UnassessedBy: filter.UnassessedBy,
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
		// The filter tag or any tag below it, like ListItems.
		subtree := s.tagGraph.Load().subtree(filter.TagId)
		hasTag := false
		for _, t := range item.Tags {
			if subtree[t.Id] {
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
	if !itemInWindow(item, filter.After) {
		return false
	}
	if !s.assessmentsMatch(item, filter.AssessorId, filter.MinScore, filter.UnassessedBy, filter.TagId, false) {
		return false
	}
	if filter.Search != "" {
		// Ask FTS5 itself, so pushed items match exactly like the initial
		// batch does and clients never have to emulate FTS tokenization.
		ok, err := db.ItemMatchesSearch(s.db, item.Id, filter.Search)
		if err != nil {
			slog.Warn("stream: search filter", "item_id", item.Id, "error", err)
			return false
		}
		return ok
	}
	return true
}

// assessmentsMatch applies the assessment filters to an item in memory, the
// twin of the SQL in db.ListItems. An assessment is in scope when there is no
// filter tag, it has no tag (a whole-item assessment), or its tag is in the
// filter tag's subtree (the tag itself when exact).
func (s *Service) assessmentsMatch(item *pb.Item, assessorID int64, minScore *float64, unassessedBy, tagID int64, exact bool) bool {
	if minScore == nil && unassessedBy == 0 {
		return true
	}
	var subtree map[int64]bool
	if tagID != 0 && !exact {
		subtree = s.tagGraph.Load().subtree(tagID)
	}
	inScope := func(a *pb.Assessment) bool {
		switch {
		case tagID == 0, a.TagId == 0:
			return true
		case exact:
			return a.TagId == tagID
		default:
			return subtree[a.TagId]
		}
	}
	scored, assessed := false, false
	for _, a := range item.Assessments {
		if !inScope(a) {
			continue
		}
		if a.AssessorId == unassessedBy && unassessedBy != 0 {
			assessed = true
		}
		if minScore != nil && a.AssessorId == assessorID && a.Score != nil && *a.Score >= *minScore {
			scored = true
		}
	}
	if minScore != nil && !scored {
		return false
	}
	return !assessed
}

// itemInWindow applies a stream filter's cutoff with the rule ListItems uses:
// the item's date is its published date, else when it was fetched, and it
// passes when that is not before the cutoff. Both sides are compared in whole
// seconds, because the database text has no fractions. An unset cutoff passes
// everything, and so does an item with no date at all.
func itemInWindow(item *pb.Item, after *timestamppb.Timestamp) bool {
	if after == nil {
		return true
	}
	d := item.Published
	if d == nil {
		d = item.FetchedAt
	}
	if d == nil {
		return true
	}
	return !d.AsTime().Truncate(time.Second).Before(after.AsTime().Truncate(time.Second))
}

// afterTime converts the optional cutoff of a request; unset is the zero time.
func afterTime(ts *timestamppb.Timestamp) (time.Time, error) {
	if ts == nil {
		return time.Time{}, nil
	}
	if err := ts.CheckValid(); err != nil {
		return time.Time{}, fmt.Errorf("after is not a valid timestamp: %v", err)
	}
	return ts.AsTime(), nil
}

// ── Conversion helpers ─────────────────────────────────────────────────────

// optionalString maps "" to nil (SQL NULL) and anything else to a pointer.
func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// firstErr returns the first non-nil error.
func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

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
	if src.Color != nil {
		p.Color = *src.Color
	}
	if src.Abbreviation != nil {
		p.Abbreviation = *src.Abbreviation
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
		Id:        t.ID,
		Name:      t.Name,
		ParentIds: t.ParentIDs,
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
	for _, a := range item.Assessments {
		p.Assessments = append(p.Assessments, dbAssessmentToProto(a))
	}
	return p
}
