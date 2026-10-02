package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func ptr[T any](v T) *T { return &v }

func TestAddSource(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)

	rec := th.do("POST", "/api/sources", `{"name":"HN","url":"https://news.ycombinator.com/rss","type":"rss",
		"refresh_sec":600,"enabled":false,"color":"#FF6600","abbreviation":"HN"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	want := &pb.AddSourceRequest{Name: "HN", Url: "https://news.ycombinator.com/rss", Type: "rss",
		RefreshSec: 600, Enabled: false, Color: "#FF6600", Abbreviation: "HN"}
	if !proto.Equal(fc.lastReq, want) {
		t.Errorf("AddSourceRequest = %v, want %v", fc.lastReq, want)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"id":"10"`) {
		t.Errorf("response lacks the new source's string id: %s", body)
	}

	// proto3's enabled defaults to false; an absent enabled means true.
	if rec := th.do("POST", "/api/sources", `{"name":"x","url":"https://x.example/feed"}`, nil); rec.Code != http.StatusCreated {
		t.Fatalf("add minimal: %d %s", rec.Code, rec.Body)
	}
	if got := fc.lastReq.(*pb.AddSourceRequest); !got.Enabled || got.Type != "" || got.RefreshSec != 0 {
		t.Errorf("minimal AddSourceRequest = %v, want enabled and daemon defaults", got)
	}

	// The response is sanitized like every other source.
	rec = th.do("POST", "/api/sources", `{"name":"x","url":"https://x.example/feed","color":"red;x:url(y)"}`, nil)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "url(y)") {
		t.Errorf("unsafe color passed through: %d %s", rec.Code, rec.Body)
	}
}

func TestUpdateSourcePresence(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)

	for _, tc := range []struct {
		body string
		want *pb.UpdateSourceRequest
	}{
		{`{}`, &pb.UpdateSourceRequest{Id: 7}},
		{``, &pb.UpdateSourceRequest{Id: 7}},
		{`{"name":"New"}`, &pb.UpdateSourceRequest{Id: 7, Name: ptr("New")}},
		// false and "" are values, not "unset".
		{`{"enabled":false}`, &pb.UpdateSourceRequest{Id: 7, Enabled: ptr(false)}},
		{`{"color":"","abbreviation":""}`, &pb.UpdateSourceRequest{Id: 7, Color: ptr(""), Abbreviation: ptr("")}},
		{`{"url":"https://a.example/f","type":"atom","refresh_sec":60,"enabled":true}`, &pb.UpdateSourceRequest{
			Id: 7, Url: ptr("https://a.example/f"), Type: ptr("atom"), RefreshSec: ptr(int32(60)), Enabled: ptr(true)}},
	} {
		rec := th.do("PATCH", "/api/sources/7", tc.body, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", tc.body, rec.Code, rec.Body)
			continue
		}
		if !proto.Equal(fc.lastReq, tc.want) {
			t.Errorf("%s: UpdateSourceRequest = %v, want %v", tc.body, fc.lastReq, tc.want)
		}
	}
}

func TestRemoveSourceAndTag(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)
	for _, target := range []string{"/api/sources/9007199254740993", "/api/tags/4", "/api/rules/2"} {
		if rec := th.do("DELETE", target, "", nil); rec.Code != http.StatusNoContent {
			t.Errorf("DELETE %s: %d %s", target, rec.Code, rec.Body)
		}
	}
	// The app's own request helper always sends {}.
	if rec := th.do("DELETE", "/api/tags/5", "{}", nil); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE with {}: %d %s", rec.Code, rec.Body)
	}
	want := []string{"source/9007199254740993", "tag/4", "rule/2", "tag/5"}
	if strings.Join(fc.removed, " ") != strings.Join(want, " ") {
		t.Errorf("removed = %v, want %v", fc.removed, want)
	}
	if rec := th.do("DELETE", "/api/sources/1", `{"cascade":true}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("DELETE with a field: %d", rec.Code)
	}
}

func TestTags(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)

	rec := th.do("POST", "/api/tags", `{"name":"go","color":"#00ADD8"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add tag: %d %s", rec.Code, rec.Body)
	}
	if want := (&pb.AddTagRequest{Name: "go", Color: "#00ADD8"}); !proto.Equal(fc.lastReq, want) {
		t.Errorf("AddTagRequest = %v, want %v", fc.lastReq, want)
	}

	for _, tc := range []struct {
		body string
		want *pb.UpdateTagRequest
	}{
		{`{"color":"#123456"}`, &pb.UpdateTagRequest{Id: 3, Color: ptr("#123456")}},
		{`{"name":"golang"}`, &pb.UpdateTagRequest{Id: 3, Name: ptr("golang")}},
		{`{"color":""}`, &pb.UpdateTagRequest{Id: 3, Color: ptr("")}},
		{`{"parent_ids":["1","2"]}`, &pb.UpdateTagRequest{Id: 3, Parents: &pb.TagParents{Ids: []int64{1, 2}}}},
		{`{"parent_ids":[]}`, &pb.UpdateTagRequest{Id: 3, Parents: &pb.TagParents{}}},
	} {
		rec := th.do("PATCH", "/api/tags/3", tc.body, nil)
		if rec.Code != http.StatusOK || !proto.Equal(fc.lastReq, tc.want) {
			t.Errorf("%s: %d, UpdateTagRequest = %v, want %v", tc.body, rec.Code, fc.lastReq, tc.want)
		}
	}

	rec = th.do("POST", "/api/tags", `{"name":"CVE","parent_ids":["4","7"]}`, nil)
	if want := (&pb.AddTagRequest{Name: "CVE", ParentIds: []int64{4, 7}}); rec.Code != http.StatusCreated || !proto.Equal(fc.lastReq, want) {
		t.Errorf("add tag with parents: %d, AddTagRequest = %v, want %v", rec.Code, fc.lastReq, want)
	}
	var created struct {
		ParentIDs []string `json:"parent_ids"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || !reflect.DeepEqual(created.ParentIDs, []string{"4", "7"}) {
		t.Errorf("created tag parent_ids = %v (%v): %s", created.ParentIDs, err, rec.Body)
	}
	for _, body := range []string{
		`{"parent_ids":"4"}`, `{"parent_ids":[4]}`, `{"parent_ids":["x"]}`, `{"parent_ids":["0"]}`,
		`{"parent_ids":["-1"]}`, `{"parent_ids":null}`,
	} {
		if rec := th.do("PATCH", "/api/tags/3", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s: %d, want 400", body, rec.Code)
		}
	}

	rec = th.do("PATCH", "/api/tags/3", `{"color":"expression(1)"}`, nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "expression") {
		t.Errorf("unsafe tag color passed through: %s", rec.Body)
	}
}

func TestRules(t *testing.T) {
	fc := &fakeClient{rules: []*pb.TagRule{{Id: 1, TagId: 2, TagName: "rust", Field: "title", Pattern: `(?i)\brust\b`}}}
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/rules", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tag_name":"rust"`) {
		t.Fatalf("list rules: %d %s", rec.Code, rec.Body)
	}

	rec = th.do("POST", "/api/rules", `{"tag_id":"2","source_id":"9","field":"title","pattern":"(?i)go","priority":-1}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add rule: %d %s", rec.Code, rec.Body)
	}
	want := &pb.AddTagRuleRequest{TagId: 2, SourceId: 9, Field: "title", Pattern: "(?i)go", Priority: -1}
	if !proto.Equal(fc.lastReq, want) {
		t.Errorf("AddTagRuleRequest = %v, want %v", fc.lastReq, want)
	}
	// source_id "0" or absent is a global rule.
	if rec := th.do("POST", "/api/rules", `{"tag_id":"2","source_id":"0","pattern":"x"}`, nil); rec.Code != http.StatusCreated ||
		fc.lastReq.(*pb.AddTagRuleRequest).SourceId != 0 {
		t.Errorf("global rule: %d %v", rec.Code, fc.lastReq)
	}
	fc.lastReq = nil
	if rec := th.do("POST", "/api/rules", `{"pattern":"x"}`, nil); rec.Code != http.StatusBadRequest || fc.lastReq != nil {
		t.Errorf("missing tag_id: %d, RPC %v", rec.Code, fc.lastReq)
	}
}

func TestTestRule(t *testing.T) {
	fc := &fakeClient{ruleTest: &pb.TestTagRuleResponse{Scanned: 42, Items: []*pb.Item{
		{Id: 1, Title: "Rust 2.0", Link: "https://example.com/rust"},
		{Id: 2, Title: "Evil", Link: "javascript:alert(1)"},
	}}}
	th := newTestHandler(t, fc)

	rec := th.do("POST", "/api/rules/test", `{"pattern":"(?i)rust","field":"title","source_id":"3","limit":50}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("test rule: %d %s", rec.Code, rec.Body)
	}
	want := &pb.TestTagRuleRequest{Pattern: "(?i)rust", Field: "title", SourceId: 3, Limit: 50}
	if !proto.Equal(fc.lastReq, want) {
		t.Errorf("TestTagRuleRequest = %v, want %v", fc.lastReq, want)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"scanned":42`) || !strings.Contains(body, "https://example.com/rust") {
		t.Errorf("test rule body: %s", body)
	}
	if strings.Contains(body, "javascript:") {
		t.Errorf("unsafe link passed through: %s", body)
	}
	// Mutation rules apply even though it changes nothing: it is a POST.
	if rec := th.do("POST", "/api/rules/test", `{"pattern":"x"}`, map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin test: %d", rec.Code)
	}
	for _, body := range []string{`{"pattern":"x","limit":0}`, `{"pattern":"x","limit":101}`} {
		if rec := th.do("POST", "/api/rules/test", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, rec.Code)
		}
	}
}

// TestManageBodyValidation covers the shape checks every management body
// goes through before any RPC is made.
func TestManageBodyValidation(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		code                       int
	}{
		{"unknown field", "POST", "/api/sources", `{"name":"x","url":"https://x.example","extra":1}`, 400},
		{"field names are exact", "POST", "/api/sources", `{"Name":"x"}`, 400},
		{"camelCase", "PATCH", "/api/sources/1", `{"refreshSec":60}`, 400},
		{"id in body", "PATCH", "/api/sources/1", `{"id":"2"}`, 400},
		{"null", "PATCH", "/api/sources/1", `{"color":null}`, 400},
		{"duplicate", "PATCH", "/api/sources/1", `{"name":"a","name":"b"}`, 400},
		{"string for bool", "PATCH", "/api/sources/1", `{"enabled":"false"}`, 400},
		{"number for string", "PATCH", "/api/sources/1", `{"name":1}`, 400},
		{"fraction", "PATCH", "/api/sources/1", `{"refresh_sec":60.5}`, 400},
		{"int32 overflow", "PATCH", "/api/sources/1", `{"refresh_sec":2147483648}`, 400},
		{"string for int", "PATCH", "/api/sources/1", `{"refresh_sec":"60"}`, 400},
		{"array", "POST", "/api/tags", `[{"name":"x"}]`, 400},
		{"not JSON", "POST", "/api/tags", `name=x`, 400},
		{"truncated", "POST", "/api/tags", `{"name":"x"`, 400},
		{"trailing data", "POST", "/api/tags", `{"name":"x"}{"name":"y"}`, 400},
		{"number id", "POST", "/api/rules", `{"tag_id":2,"pattern":"x"}`, 400},
		{"negative id", "POST", "/api/rules", `{"tag_id":"-2","pattern":"x"}`, 400},
		{"too large", "POST", "/api/rules", `{"tag_id":"1","pattern":"` + strings.Repeat("a", maxManageBodyBytes) + `"}`, 413},
		{"bad path id", "PATCH", "/api/sources/abc", `{}`, 400},
		{"zero path id", "DELETE", "/api/tags/0", ``, 400},
		{"negative path id", "DELETE", "/api/rules/-1", ``, 400},
		{"huge path id", "PATCH", "/api/tags/9223372036854775808", `{}`, 400},
		{"no id", "PATCH", "/api/sources/", `{}`, 404},
		{"no rule update", "PATCH", "/api/rules/1", `{}`, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{}
			th := newTestHandler(t, fc)
			rec := th.do(tc.method, tc.target, tc.body, nil)
			if rec.Code != tc.code {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.code, rec.Body)
			}
			if fc.lastReq != nil || len(fc.removed) > 0 {
				t.Errorf("RPC made for a rejected request: %v %v", fc.lastReq, fc.removed)
			}
			if _, ok := decode(t, rec)["error"]; !ok {
				t.Errorf("no error message: %s", rec.Body)
			}
		})
	}
}

// TestManageCSRF checks every new mutation against the CSRF and Host rules:
// nothing reaches the daemon unless the request is JSON from our origin.
func TestManageCSRF(t *testing.T) {
	mutations := []struct{ method, target, body string }{
		{"POST", "/api/sources", `{"name":"x","url":"https://x.example"}`},
		{"PATCH", "/api/sources/1", `{"enabled":false}`},
		{"DELETE", "/api/sources/1", `{}`},
		{"POST", "/api/tags", `{"name":"x"}`},
		{"PATCH", "/api/tags/1", `{"color":"#000000"}`},
		{"DELETE", "/api/tags/1", `{}`},
		{"POST", "/api/rules", `{"tag_id":"1","pattern":"x"}`},
		{"DELETE", "/api/rules/1", `{}`},
		{"POST", "/api/rules/test", `{"pattern":"x"}`},
	}
	for _, m := range mutations {
		for _, tc := range []struct {
			name string
			hdr  map[string]string
			code int
		}{
			{"cross-site origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
			{"no origin, cross-site fetch", map[string]string{"Origin": "", "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
			{"no origin, same-site fetch", map[string]string{"Origin": "", "Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
			{"no origin headers", map[string]string{"Origin": ""}, http.StatusForbidden},
			{"form content type", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
			{"text/plain", map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
			{"no content type", map[string]string{"Content-Type": ""}, http.StatusUnsupportedMediaType},
			{"rebound host", map[string]string{"Host": "attacker.example"}, http.StatusMisdirectedRequest},
		} {
			fc := &fakeClient{}
			th := newTestHandler(t, fc)
			rec := th.do(m.method, m.target, m.body, tc.hdr)
			if rec.Code != tc.code {
				t.Errorf("%s %s, %s: status %d, want %d", m.method, m.target, tc.name, rec.Code, tc.code)
			}
			if fc.lastReq != nil || len(fc.removed) > 0 {
				t.Errorf("%s %s, %s: reached the daemon", m.method, m.target, tc.name)
			}
		}

		// Same-origin fetch metadata is enough without an Origin header.
		fc := &fakeClient{}
		th := newTestHandler(t, fc)
		rec := th.do(m.method, m.target, m.body, map[string]string{"Origin": "", "Sec-Fetch-Site": "same-origin"})
		if rec.Code >= 300 {
			t.Errorf("%s %s, same-origin: status %d %s", m.method, m.target, rec.Code, rec.Body)
		}
	}
}

func TestManageRPCErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{status.Error(codes.InvalidArgument, "refresh_sec must be between 60 and 604800, got 5"), http.StatusBadRequest},
		{status.Error(codes.AlreadyExists, `a source with url "x" already exists`), http.StatusConflict},
		{status.Error(codes.NotFound, "source 7 not found"), http.StatusNotFound},
		{errUnavailable, http.StatusServiceUnavailable},
	} {
		fc := &fakeClient{err: tc.err}
		th := newTestHandler(t, fc)
		rec := th.do("PATCH", "/api/sources/7", `{"refresh_sec":5}`, nil)
		if rec.Code != tc.code {
			t.Errorf("%v: status %d, want %d", tc.err, rec.Code, tc.code)
		}
		// The daemon's message is what the form shows next to the field.
		if msg := decode(t, rec)["error"]; msg != status.Convert(tc.err).Message() {
			t.Errorf("%v: error %q", tc.err, msg)
		}
	}
}

// A delete of an ID the daemon doesn't know is a 404 with the daemon's
// message, not a 204.
func TestRemoveUnknownIsNotFound(t *testing.T) {
	for _, tc := range []struct {
		target string
		msg    string
	}{
		{"/api/sources/42", "source 42 not found"},
		{"/api/tags/42", "tag 42 not found"},
		{"/api/rules/42", "tag rule 42 not found"},
	} {
		fc := &fakeClient{err: status.Error(codes.NotFound, tc.msg)}
		th := newTestHandler(t, fc)
		rec := th.do("DELETE", tc.target, "", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("DELETE %s: status %d %s, want 404", tc.target, rec.Code, rec.Body)
			continue
		}
		if got := decode(t, rec)["error"]; got != tc.msg {
			t.Errorf("DELETE %s: error %q, want %q", tc.target, got, tc.msg)
		}
		if len(fc.removed) != 0 {
			t.Errorf("DELETE %s: recorded removals %v on a failed call", tc.target, fc.removed)
		}
	}
}
