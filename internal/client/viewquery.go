package client

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/since"
)

// ── Saved views as queries ──
//
// A saved view is resolved on the client into a plain filter: the daemon's
// Search and StreamItems know nothing about views. This file is that
// resolution, shared by the CLI (`nyttig search -view`) and nyttig-api
// (`GET /api/items?view=` and `/api/stream?view=`) so the two cannot drift.

// ErrViewNotFound is wrapped by FindView when no view has the reference.
var ErrViewNotFound = errors.New("view not found")

// FindView picks a view out of views by ID (a numeric ref that matches one)
// or by name, ignoring case like the daemon's uniqueness rule does.
func FindView(views []*pb.SavedView, ref string) (*pb.SavedView, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, v := range views {
			if v.Id == id {
				return v, nil
			}
		}
	}
	for _, v := range views {
		if strings.EqualFold(v.Name, ref) {
			return v, nil
		}
	}
	return nil, fmt.Errorf("view %q: %w", ref, ErrViewNotFound)
}

// CutoffFromSince resolves a window such as "7d" to the absolute cutoff the
// daemon's queries take, counted back from now and never before the Unix
// epoch (a window like 9999y reaches before year 1, which a Timestamp cannot
// hold). An empty window is no cutoff (nil).
func CutoffFromSince(text string, now time.Time) (*timestamppb.Timestamp, error) {
	if text == "" {
		return nil, nil
	}
	w, err := since.Parse(text)
	if err != nil {
		return nil, err
	}
	cut := w.Cutoff(now)
	if cut.Before(time.Unix(0, 0)) {
		cut = time.Unix(0, 0)
	}
	return timestamppb.New(cut), nil
}

// Overrides are the filter settings a caller passes explicitly. Each one that
// is set replaces the view's value for that field; unset (nil) keeps the
// view's. A set 0 (source, tag, assessor, unassessed) clears the view's part,
// a set Since of "" removes its window and NoMinScore drops its minimum.
// Clearing the assessor takes the minimum and a score sort with it (the
// daemon refuses them without one) unless they are overridden too.
type Overrides struct {
	Query        *string
	SourceID     *int64
	TagID        *int64
	Sort         *string
	UnviewedOnly *bool
	// Since is a window such as "7d" counted back from now; "" removes the
	// view's window. After, an absolute cutoff, wins over Since.
	Since        *string
	After        *timestamppb.Timestamp
	AssessorID   *int64
	MinScore     *float64
	NoMinScore   bool // drop the view's minimum score
	UnassessedBy *int64
}

// ViewSearchRequest builds the SearchRequest a view stands for, with the
// overrides applied field by field. The view's window ("7d") becomes the
// absolute cutoff now minus the window; now is taken once by the caller, so a
// whole snapshot shares one cutoff. Paging (Limit, Offset) and TagExact are
// the caller's to set. A nil view applies only the overrides.
func ViewSearchRequest(v *pb.SavedView, o Overrides, now time.Time) (*pb.SearchRequest, error) {
	req := &pb.SearchRequest{}
	window := ""
	if f := v.GetFilter(); f != nil {
		req.Query = f.Search
		req.SourceId = f.SourceId
		req.TagId = f.TagId
		req.Sort = f.Sort
		req.UnviewedOnly = f.UnviewedOnly
		req.AssessorId = f.AssessorId
		req.MinScore = f.MinScore
		req.UnassessedBy = f.UnassessedBy
		window = f.Since
	}
	if o.Query != nil {
		req.Query = *o.Query
	}
	if o.SourceID != nil {
		req.SourceId = *o.SourceID
	}
	if o.TagID != nil {
		req.TagId = *o.TagID
	}
	if o.Sort != nil {
		req.Sort = *o.Sort
	}
	if o.UnviewedOnly != nil {
		req.UnviewedOnly = *o.UnviewedOnly
	}
	if o.AssessorID != nil {
		req.AssessorId = *o.AssessorID
	}
	if o.AssessorID != nil && *o.AssessorID == 0 {
		req.MinScore = nil
		if req.Sort == "score" && o.Sort == nil {
			req.Sort = "newest"
		}
	}
	if o.NoMinScore {
		req.MinScore = nil
	}
	if o.MinScore != nil {
		req.MinScore = o.MinScore
	}
	if o.UnassessedBy != nil {
		req.UnassessedBy = *o.UnassessedBy
	}
	if o.Since != nil {
		window = *o.Since
	}
	switch {
	case o.After != nil:
		req.After = o.After
	default:
		after, err := CutoffFromSince(window, now)
		if err != nil {
			return nil, err
		}
		req.After = after
	}
	return req, nil
}

// StreamFilterOf is the StreamFilter with the same filter as a SearchRequest
// (limit left to the caller), so a view can feed a stream the same way.
func StreamFilterOf(req *pb.SearchRequest) *pb.StreamFilter {
	return &pb.StreamFilter{
		SourceId:     req.SourceId,
		TagId:        req.TagId,
		Search:       req.Query,
		Sort:         req.Sort,
		UnviewedOnly: req.UnviewedOnly,
		After:        req.After,
		AssessorId:   req.AssessorId,
		MinScore:     req.MinScore,
		UnassessedBy: req.UnassessedBy,
	}
}
