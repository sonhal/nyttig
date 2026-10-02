package api

import (
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// viewNow is the fixed clock of the ?view= tests.
var viewNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func newViewHandler(t *testing.T, fc *fakeClient) *testHandler {
	t.Helper()
	h, err := New(Config{Client: fc, Origin: testOrigin, Now: func() time.Time { return viewNow }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &testHandler{h: h}
}

func workView() *pb.SavedView {
	min := 0.5
	return &pb.SavedView{Id: 12, Name: "claude-cve", Filter: &pb.ViewFilter{
		Search: "openssl", TagId: 4, Sort: "score", Since: "1d",
		AssessorId: 3, MinScore: &min, UnassessedBy: 3,
	}}
}

func TestItemsByView(t *testing.T) {
	fc := &fakeClient{search: &pb.SearchResponse{}, views: []*pb.SavedView{
		{Id: 1, Name: "other"}, workView(), {Id: 13, Name: "12"},
	}}
	th := newViewHandler(t, fc)
	wantAfter := viewNow.Add(-24 * time.Hour).Unix()

	check := func(label, target string) {
		t.Helper()
		fc.lastSrch = nil
		if rec := th.do("GET", target, "", nil); rec.Code != 200 {
			t.Fatalf("%s: status %d: %s", label, rec.Code, rec.Body)
		}
		r := fc.lastSrch
		if r == nil || r.Query != "openssl" || r.TagId != 4 || r.Sort != "score" || r.AssessorId != 3 ||
			r.MinScore == nil || *r.MinScore != 0.5 || r.UnassessedBy != 3 {
			t.Errorf("%s: SearchRequest = %v", label, r)
			return
		}
		// The window is now minus one day, whole seconds.
		if r.After.GetSeconds() != wantAfter || r.After.GetNanos() != 0 {
			t.Errorf("%s: after = %v, want %d", label, r.After, wantAfter)
		}
		if r.Limit != 100 || r.Offset != 0 {
			t.Errorf("%s: paging %d/%d", label, r.Limit, r.Offset)
		}
	}
	check("by name", "/api/items?view=claude-cve")
	check("name in any case", "/api/items?view=CLAUDE-Cve")
	check("by ID", "/api/items?view=12")
	// An ID wins over a view whose name looks like that number.
	check("ID over a numeric name", "/api/items?view=12&limit=100")

	// Paging and tag_exact stay the request's.
	fc.lastSrch = nil
	if rec := th.do("GET", "/api/items?view=claude-cve&limit=5&offset=10&tag_exact=1", "", nil); rec.Code != 200 ||
		fc.lastSrch.Limit != 5 || fc.lastSrch.Offset != 10 || !fc.lastSrch.TagExact {
		t.Errorf("paging: %d %v", rec.Code, fc.lastSrch)
	}
}

func TestItemsByViewUnknown(t *testing.T) {
	fc := &fakeClient{search: &pb.SearchResponse{}, views: []*pb.SavedView{workView()}}
	th := newViewHandler(t, fc)
	for _, target := range []string{"/api/items?view=nope", "/api/items?view=99", "/api/stream?view=nope"} {
		fc.lastSrch = nil
		rec := th.do("GET", target, "", nil)
		if rec.Code != http.StatusNotFound || fc.lastSrch != nil {
			t.Errorf("%s: status %d (Search called: %v), want 404 and no search", target, rec.Code, fc.lastSrch != nil)
		}
	}
	// A view reference that is too long is a bad parameter, not a lookup.
	long := make([]byte, maxViewRef+1)
	for i := range long {
		long[i] = 'a'
	}
	if rec := th.do("GET", "/api/items?view="+string(long), "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("long view: %d", rec.Code)
	}
	// The daemon failing to list views is not a 404.
	fc.err = status.Error(codes.Unavailable, "down")
	if rec := th.do("GET", "/api/items?view=claude-cve", "", nil); rec.Code == http.StatusNotFound || rec.Code < 500 {
		t.Errorf("daemon down: %d", rec.Code)
	}
}

func TestItemsByViewOverrides(t *testing.T) {
	fc := &fakeClient{search: &pb.SearchResponse{}, views: []*pb.SavedView{workView()}}
	th := newViewHandler(t, fc)

	get := func(q string) *pb.SearchRequest {
		t.Helper()
		fc.lastSrch = nil
		if rec := th.do("GET", "/api/items?view=claude-cve"+q, "", nil); rec.Code != 200 {
			t.Fatalf("%s: status %d: %s", q, rec.Code, rec.Body)
		}
		return fc.lastSrch
	}

	// Explicit parameters replace the view's field, the rest stays.
	r := get("&unassessed=8&q=kernel&tag=0&sort=newest")
	if r.UnassessedBy != 8 || r.Query != "kernel" || r.TagId != 0 || r.Sort != "newest" || r.AssessorId != 3 || *r.MinScore != 0.5 {
		t.Errorf("overrides: %v", r)
	}
	r = get("&assessor=7&min_score=0.9")
	if r.AssessorId != 7 || *r.MinScore != 0.9 || r.UnassessedBy != 3 || r.TagId != 4 {
		t.Errorf("assessor override: %v", r)
	}
	// 0 clears the view's "not assessed by".
	if r = get("&unassessed=0"); r.UnassessedBy != 0 || r.AssessorId != 3 {
		t.Errorf("unassessed=0: %v", r)
	}
	// unviewed=0 beats a view that sets it; absent keeps it.
	fc.views[0].Filter.UnviewedOnly = true
	if r = get(""); !r.UnviewedOnly {
		t.Errorf("view's unviewed lost: %v", r)
	}
	if r = get("&unviewed=0"); r.UnviewedOnly {
		t.Errorf("unviewed=0 did not override: %v", r)
	}
	// An explicit after= beats the view's window; there is no since parameter.
	if r = get("&after=1788264000"); r.After.GetSeconds() != 1788264000 {
		t.Errorf("after override: %v", r.After)
	}
	wantAfter := viewNow.Add(-24 * time.Hour).Unix()
	if r = get("&since=7d"); r.After.GetSeconds() != wantAfter {
		t.Errorf("since is not a parameter, the view's window stays: %v", r.After)
	}
	// Bad values are still 400s, with the view in place.
	for _, q := range []string{"&min_score=2", "&assessor=x", "&after=abc", "&sort=best"} {
		if rec := th.do("GET", "/api/items?view=claude-cve"+q, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

func TestItemsByViewWindowEdges(t *testing.T) {
	fc := &fakeClient{search: &pb.SearchResponse{}, views: []*pb.SavedView{
		{Id: 1, Name: "plain", Filter: &pb.ViewFilter{TagId: 2}},
		{Id: 2, Name: "huge", Filter: &pb.ViewFilter{Since: "9999y"}},
		{Id: 3, Name: "broken", Filter: &pb.ViewFilter{Since: "soon"}},
		{Id: 4, Name: "empty"},
	}}
	th := newViewHandler(t, fc)
	if rec := th.do("GET", "/api/items?view=plain", "", nil); rec.Code != 200 || fc.lastSrch.After != nil || fc.lastSrch.TagId != 2 {
		t.Errorf("plain: %d %v", rec.Code, fc.lastSrch)
	}
	if rec := th.do("GET", "/api/items?view=empty", "", nil); rec.Code != 200 || fc.lastSrch.After != nil {
		t.Errorf("view without a filter: %d %v", rec.Code, fc.lastSrch)
	}
	if rec := th.do("GET", "/api/items?view=huge", "", nil); rec.Code != 200 || fc.lastSrch.After.GetSeconds() != 0 {
		t.Errorf("huge window is clamped to the epoch: %d %v", rec.Code, fc.lastSrch.After)
	}
	if rec := th.do("GET", "/api/items?view=broken", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unparsable stored window: %d", rec.Code)
	}
}

func TestStreamByView(t *testing.T) {
	fs := newFakeStream(4)
	fs.msgs <- completeMsg
	fc := &fakeClient{stream: fs, views: []*pb.SavedView{workView()}}
	srv := startStreamServer(t, fc, Config{PingInterval: 50 * time.Millisecond, Now: func() time.Time { return viewNow }})

	resp, cancel := getStream(t, srv, "?view=claude-cve&unassessed=8&unviewed=1")
	defer cancel()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	f := fs.filter()
	if f == nil {
		t.Fatal("no filter sent to the daemon")
	}
	if f.Search != "openssl" || f.TagId != 4 || f.Sort != "score" || f.AssessorId != 3 ||
		f.MinScore == nil || *f.MinScore != 0.5 || f.UnassessedBy != 8 || !f.UnviewedOnly {
		t.Errorf("stream filter = %v", f)
	}
	if want := viewNow.Add(-24 * time.Hour).Unix(); f.GetAfter().GetSeconds() != want {
		t.Errorf("after = %v, want %d", f.GetAfter(), want)
	}

	// An unknown view is a 404 before any stream is opened.
	resp2, cancel2 := getStream(t, srv, "?view=nope")
	defer cancel2()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("unknown view: status %d", resp2.StatusCode)
	}
}

// A shared reading view also feeds an assessor, so a parameter that is
// present but empty (or false) clears the view's field.
func TestItemsByViewClearing(t *testing.T) {
	fc := &fakeClient{search: &pb.SearchResponse{}, views: []*pb.SavedView{workView()}}
	fc.views[0].Filter.UnviewedOnly = true
	th := newViewHandler(t, fc)
	get := func(q string) *pb.SearchRequest {
		t.Helper()
		fc.lastSrch = nil
		if rec := th.do("GET", "/api/items?view=claude-cve"+q, "", nil); rec.Code != 200 {
			t.Fatalf("%s: status %d: %s", q, rec.Code, rec.Body)
		}
		return fc.lastSrch
	}
	for _, q := range []string{"&unviewed=0", "&unviewed=false", "&unviewed="} {
		if r := get(q); r.UnviewedOnly || r.Query != "openssl" {
			t.Errorf("%s: %v", q, r)
		}
	}
	if r := get("&min_score="); r.MinScore != nil || r.AssessorId != 3 || r.Sort != "score" {
		t.Errorf("min_score=: %v", r)
	}
	// Clearing the assessor takes the minimum and the score sort with it.
	if r := get("&assessor="); r.AssessorId != 0 || r.MinScore != nil || r.Sort != "newest" || r.UnassessedBy != 3 {
		t.Errorf("assessor=: %v", r)
	}
	// ...unless they are overridden explicitly.
	if r := get("&assessor=&sort=oldest"); r.Sort != "oldest" || r.MinScore != nil {
		t.Errorf("assessor=&sort=oldest: %v", r)
	}
	if r := get("&unassessed="); r.UnassessedBy != 0 || r.AssessorId != 3 {
		t.Errorf("unassessed=: %v", r)
	}
	if r := get("&after="); r.After != nil {
		t.Errorf("after=: %v", r.After)
	}
	if r := get("&sort=newest"); r.Sort != "newest" || r.AssessorId != 3 {
		t.Errorf("sort=newest: %v", r)
	}
	if r := get("&q=&tag=&source="); r.Query != "" || r.TagId != 0 {
		t.Errorf("q=&tag=: %v", r)
	}
	// The same clears work on the stream.
	fs := newFakeStream(4)
	fs.msgs <- completeMsg
	srv := startStreamServer(t, &fakeClient{stream: fs, views: fc.views}, Config{PingInterval: 50 * time.Millisecond, Now: func() time.Time { return viewNow }})
	resp, cancel := getStream(t, srv, "?view=claude-cve&unviewed=0&min_score=&unassessed=&after=")
	defer cancel()
	f := fs.filter()
	if resp.StatusCode != 200 || f == nil || f.UnviewedOnly || f.MinScore != nil || f.UnassessedBy != 0 || f.After != nil || f.AssessorId != 3 {
		t.Errorf("stream clearing: %d %v", resp.StatusCode, f)
	}
}
