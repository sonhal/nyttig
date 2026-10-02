package client

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestFindView(t *testing.T) {
	views := []*pb.SavedView{{Id: 1, Name: "Security"}, {Id: 7, Name: "claude-cve"}, {Id: 9, Name: "7"}}
	for _, tc := range []struct {
		ref    string
		wantID int64
		ok     bool
	}{
		{"1", 1, true},
		{"security", 1, true}, // names ignore case
		{"CLAUDE-CVE", 7, true},
		{"7", 7, true}, // an ID wins over a name that looks like a number
		{"9", 9, true},
		{"nope", 0, false},
		{"2", 0, false},
	} {
		v, err := FindView(views, tc.ref)
		if (err == nil) != tc.ok || (tc.ok && v.Id != tc.wantID) {
			t.Errorf("FindView(%q) = %v, %v", tc.ref, v, err)
		}
		if err != nil && !errors.Is(err, ErrViewNotFound) {
			t.Errorf("FindView(%q) error %v does not wrap ErrViewNotFound", tc.ref, err)
		}
	}
	// A numeric ref no view has by ID falls back to the name.
	v, err := FindView([]*pb.SavedView{{Id: 3, Name: "2024"}}, "2024")
	if err != nil || v.Id != 3 {
		t.Errorf("name that looks like a number: %v, %v", v, err)
	}
}

func TestCutoffFromSince(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	got, err := CutoffFromSince("7d", now)
	if err != nil || !got.AsTime().Equal(time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("7d = %v, %v", got, err)
	}
	if got, err := CutoffFromSince("9999y", now); err != nil || got.AsTime().Unix() != 0 {
		t.Errorf("9999y = %v, %v; want the epoch", got, err)
	}
	if got, err := CutoffFromSince("", now); got != nil || err != nil {
		t.Errorf("empty = %v, %v; want nil, nil", got, err)
	}
	for _, bad := range []string{"1m", "7", "x", "0d"} {
		if _, err := CutoffFromSince(bad, now); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestViewSearchRequest(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	min := 0.7
	view := &pb.SavedView{Id: 5, Name: "claude-cve", Filter: &pb.ViewFilter{
		Search: "openssl", SourceId: 2, TagId: 4, Sort: "score", UnviewedOnly: true,
		Since: "1d", AssessorId: 3, MinScore: &min, UnassessedBy: 3,
	}}

	// The view alone: every field, the window as now minus one day.
	req, err := ViewSearchRequest(view, Overrides{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if req.Query != "openssl" || req.SourceId != 2 || req.TagId != 4 || req.Sort != "score" || !req.UnviewedOnly ||
		req.AssessorId != 3 || req.MinScore == nil || *req.MinScore != 0.7 || req.UnassessedBy != 3 {
		t.Errorf("view fields: %+v", req)
	}
	if want := now.Add(-24 * time.Hour); !req.After.AsTime().Equal(want) {
		t.Errorf("after = %v, want %v", req.After.AsTime(), want)
	}

	// Overrides win field by field; what is not overridden stays the view's.
	str := func(s string) *string { return &s }
	i64 := func(n int64) *int64 { return &n }
	no := false
	other := 0.2
	req, err = ViewSearchRequest(view, Overrides{
		Query: str("kernel"), TagID: i64(0), Sort: str("newest"), UnviewedOnly: &no,
		UnassessedBy: i64(8), MinScore: &other, Since: str("7d"),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if req.Query != "kernel" || req.TagId != 0 || req.Sort != "newest" || req.UnviewedOnly ||
		req.UnassessedBy != 8 || *req.MinScore != 0.2 || req.SourceId != 2 || req.AssessorId != 3 {
		t.Errorf("overridden: %+v", req)
	}
	if want := now.Add(-7 * 24 * time.Hour); !req.After.AsTime().Equal(want) {
		t.Errorf("after = %v, want %v", req.After.AsTime(), want)
	}

	// An empty Since removes the view's window; an absolute After beats both.
	req, _ = ViewSearchRequest(view, Overrides{Since: str("")}, now)
	if req.After != nil {
		t.Errorf("empty since kept a window: %v", req.After)
	}
	abs := timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	req, _ = ViewSearchRequest(view, Overrides{After: abs, Since: str("7d")}, now)
	if !req.After.AsTime().Equal(abs.AsTime()) {
		t.Errorf("after = %v, want the explicit cutoff", req.After.AsTime())
	}

	// Clearing: no minimum, and clearing the assessor takes the minimum and
	// the score sort along.
	req, _ = ViewSearchRequest(view, Overrides{NoMinScore: true}, now)
	if req.MinScore != nil || req.AssessorId != 3 || req.Sort != "score" {
		t.Errorf("NoMinScore: %+v", req)
	}
	req, _ = ViewSearchRequest(view, Overrides{AssessorID: i64(0)}, now)
	if req.AssessorId != 0 || req.MinScore != nil || req.Sort != "newest" {
		t.Errorf("assessor cleared: %+v", req)
	}
	req, _ = ViewSearchRequest(view, Overrides{AssessorID: i64(0), Sort: str("oldest")}, now)
	if req.Sort != "oldest" {
		t.Errorf("explicit sort kept: %+v", req)
	}

	// A bad window is an error; a nil view applies just the overrides.
	if _, err := ViewSearchRequest(&pb.SavedView{Filter: &pb.ViewFilter{Since: "soon"}}, Overrides{}, now); err == nil {
		t.Error("bad window accepted")
	}
	req, err = ViewSearchRequest(nil, Overrides{Query: str("x")}, now)
	if err != nil || req.Query != "x" || req.After != nil {
		t.Errorf("nil view: %+v, %v", req, err)
	}

	sf := StreamFilterOf(&pb.SearchRequest{Query: "q", TagId: 4, AssessorId: 3, MinScore: &min, UnassessedBy: 3, After: abs, Sort: "score", UnviewedOnly: true, SourceId: 2})
	if sf.Search != "q" || sf.TagId != 4 || sf.AssessorId != 3 || *sf.MinScore != 0.7 || sf.UnassessedBy != 3 ||
		sf.After != abs || sf.Sort != "score" || !sf.UnviewedOnly || sf.SourceId != 2 {
		t.Errorf("stream filter: %+v", sf)
	}
}
