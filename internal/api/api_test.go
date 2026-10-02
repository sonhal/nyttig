package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// testHandler sends requests the way the app's own page would: to the
// configured host, and with a same-origin Origin and JSON body on
// mutations. Tests override headers to break those rules.
type testHandler struct {
	h http.Handler
}

func (th *testHandler) do(method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "nyttig.example.com"
	if !isSafeMethod(method) {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", testOrigin)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	th.h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return m
}

func TestHealth(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)
	rec := th.do("GET", "/api/health", "", nil)
	if rec.Code != 200 || decode(t, rec)["ok"] != true {
		t.Fatalf("healthy daemon: %d %s", rec.Code, rec.Body)
	}

	fc.err = errUnavailable
	rec = th.do("GET", "/api/health", "", nil)
	if rec.Code != http.StatusServiceUnavailable || decode(t, rec)["ok"] != false {
		t.Fatalf("daemon down: %d %s", rec.Code, rec.Body)
	}
}

func TestListSourcesAndTags(t *testing.T) {
	fc := &fakeClient{
		sources: []*pb.Source{
			{Id: 9007199254740993, Name: "HN", Color: "#FF6600", RefreshSec: 600, Enabled: true},
			{Id: 2, Name: "Evil", Color: "red;background:url(x)"},
		},
		tags: []*pb.Tag{{Id: 1, Name: "go", Color: "#00ADD8"}, {Id: 2, Name: "bad", Color: "expression(1)"}},
	}
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/sources", "", nil)
	if rec.Code != 200 {
		t.Fatalf("sources: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	// int64 is a string in protojson, so the ID survives JavaScript numbers.
	if !strings.Contains(body, `"9007199254740993"`) {
		t.Errorf("int64 id not encoded as string: %s", body)
	}
	for _, want := range []string{`"refresh_sec"`, `"#FF6600"`} {
		if !strings.Contains(body, want) {
			t.Errorf("sources body lacks %s: %s", want, body)
		}
	}
	if strings.Contains(body, "url(x)") {
		t.Errorf("invalid source color was passed through: %s", body)
	}

	rec = th.do("GET", "/api/tags", "", nil)
	if rec.Code != 200 {
		t.Fatalf("tags: %d %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"#00ADD8"`) || strings.Contains(body, "expression") {
		t.Errorf("tags body: %s", body)
	}
}

func TestRefresh(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)
	if rec := th.do("POST", "/api/refresh", "{}", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("refresh all: %d %s", rec.Code, rec.Body)
	}
	if rec := th.do("POST", "/api/refresh?source=7", "{}", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("refresh one: %d %s", rec.Code, rec.Body)
	}
	if rec := th.do("POST", "/api/refresh?source=abc", "{}", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", rec.Code)
	}
	if rec := th.do("GET", "/api/refresh", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("GET refresh: %d", rec.Code)
	}
	if got := fc.refreshed; len(got) != 2 || got[0] != 0 || got[1] != 7 {
		t.Errorf("refreshed = %v, want [0 7]", got)
	}
}

func TestItems(t *testing.T) {
	fc := &fakeClient{search: &pb.SearchResponse{Total: 3, Items: []*pb.Item{
		{Id: 1, Title: "ok", Link: "https://example.com/a"},
		{Id: 2, Title: "js", Link: "javascript:alert(1)"},
		{Id: 3, Title: "data", Link: "data:text/html,<script>alert(1)</script>"},
	}}}
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/items?q=kernel+panic&source=3&tag=4&sort=oldest&unviewed=1&limit=50&offset=100", "", nil)
	if rec.Code != 200 {
		t.Fatalf("items: %d %s", rec.Code, rec.Body)
	}
	want := &pb.SearchRequest{Query: "kernel panic", SourceId: 3, TagId: 4, Sort: "oldest", UnviewedOnly: true, Limit: 50, Offset: 100}
	got := fc.lastSrch
	if got.Query != want.Query || got.SourceId != want.SourceId || got.TagId != want.TagId || got.Sort != want.Sort ||
		got.UnviewedOnly != want.UnviewedOnly || got.Limit != want.Limit || got.Offset != want.Offset {
		t.Errorf("SearchRequest = %v, want %v", got, want)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "https://example.com/a") {
		t.Errorf("safe link dropped: %s", body)
	}
	if strings.Contains(body, "javascript:") || strings.Contains(body, "data:text") {
		t.Errorf("unsafe link passed through: %s", body)
	}

	if rec := th.do("GET", "/api/items?tag=4&tag_exact=1", "", nil); rec.Code != 200 || !fc.lastSrch.TagExact || fc.lastSrch.TagId != 4 {
		t.Errorf("tag_exact: %d, SearchRequest = %v", rec.Code, fc.lastSrch)
	}
	if rec := th.do("GET", "/api/items?tag=4", "", nil); rec.Code != 200 || fc.lastSrch.TagExact {
		t.Errorf("tag without tag_exact: %d, SearchRequest = %v", rec.Code, fc.lastSrch)
	}

	// Defaults.
	if rec := th.do("GET", "/api/items", "", nil); rec.Code != 200 || fc.lastSrch.Limit != 100 || fc.lastSrch.Offset != 0 {
		t.Errorf("defaults: %d limit=%d offset=%d", rec.Code, fc.lastSrch.Limit, fc.lastSrch.Offset)
	}

	for _, q := range []string{
		"source=-1", "tag=x", "tag_exact=maybe", "sort=random", "unviewed=maybe", "limit=0", "limit=501", "offset=-1",
		"q=" + strings.Repeat("a", maxQueryLen+1),
	} {
		if rec := th.do("GET", "/api/items?"+q, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, rec.Code)
		}
	}
}

func TestViewed(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)

	if rec := th.do("POST", "/api/viewed", `{"ids":["1","9007199254740993"]}`, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("viewed: %d %s", rec.Code, rec.Body)
	}
	if len(fc.viewed) != 1 || fc.viewed[0][1] != 9007199254740993 {
		t.Fatalf("MarkViewed calls = %v", fc.viewed)
	}
	// An empty batch is accepted without an RPC.
	if rec := th.do("POST", "/api/viewed", `{"ids":[]}`, nil); rec.Code != http.StatusNoContent || len(fc.viewed) != 1 {
		t.Fatalf("empty batch: %d, calls %d", rec.Code, len(fc.viewed))
	}

	tooMany := `{"ids":[` + strings.Repeat(`"1",`, maxViewedIDs) + `"1"]}`
	for name, body := range map[string]string{
		"numbers":  `{"ids":[1,2]}`,
		"negative": `{"ids":["-1"]}`,
		"garbage":  `{"ids":["x"]}`,
		"unknown":  `{"ids":[],"extra":1}`,
		"not json": `ids=1`,
		"too many": tooMany,
	} {
		if rec := th.do("POST", "/api/viewed", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

func TestRPCErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		want int
	}{
		{codes.InvalidArgument, 400},
		{codes.NotFound, 404},
		{codes.AlreadyExists, 409},
		{codes.Unavailable, 503},
		{codes.DeadlineExceeded, 504},
		{codes.Internal, 502},
	} {
		fc := &fakeClient{err: status.Error(tc.code, "boom")}
		rec := newTestHandler(t, fc).do("GET", "/api/sources", "", nil)
		if rec.Code != tc.want {
			t.Errorf("%v: status %d, want %d", tc.code, rec.Code, tc.want)
		}
		if decode(t, rec)["error"] != "boom" {
			t.Errorf("%v: body %s", tc.code, rec.Body)
		}
	}
}

func TestSafeLinkAndColor(t *testing.T) {
	for link, want := range map[string]bool{
		"https://example.com/a?b#c": true,
		"http://example.com":        true,
		"HTTPS://EXAMPLE.COM":       true,
		"javascript:alert(1)":       false,
		"JaVaScRiPt:alert(1)":       false,
		"data:text/html,x":          false,
		"//example.com/x":           false,
		"/relative":                 false,
		"ftp://example.com":         false,
		"https:///nohost":           false,
		"":                          false,
	} {
		if got := safeLink(link) != ""; got != want {
			t.Errorf("safeLink(%q) kept=%v, want %v", link, got, want)
		}
	}
	for c, want := range map[string]bool{
		"#aBcDeF": true, "#000000": true, "#abc": false, "red": false, "#abcdefg": false, "#abcdef;": false, "": false,
	} {
		if got := safeColor(c) != ""; got != want {
			t.Errorf("safeColor(%q) kept=%v, want %v", c, got, want)
		}
	}
}

func TestSanitizeItemCopies(t *testing.T) {
	in := &pb.Item{Id: 1, Link: "javascript:x", Tags: []*pb.Tag{{Name: "t", Color: "bad"}}}
	out := sanitizeItem(in)
	if out.Link != "" || out.Tags[0].Color != "" {
		t.Errorf("sanitized = %v", out)
	}
	if in.Link != "javascript:x" || in.Tags[0].Color != "bad" {
		t.Error("sanitizeItem modified its input, which may be shared")
	}
}
