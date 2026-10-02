package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func (f *fakeClient) ListAssessors(context.Context, *emptypb.Empty, ...grpc.CallOption) (*pb.ListAssessorsResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &pb.ListAssessorsResponse{Assessors: f.assessors}, nil
}

func (f *fakeClient) AddAssessor(_ context.Context, req *pb.AddAssessorRequest, _ ...grpc.CallOption) (*pb.Assessor, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	return &pb.Assessor{Id: 4, Name: req.Name, Description: req.Description, Color: req.Color}, nil
}

func (f *fakeClient) UpdateAssessor(_ context.Context, req *pb.UpdateAssessorRequest, _ ...grpc.CallOption) (*pb.Assessor, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	return &pb.Assessor{Id: req.Id, Name: req.GetName(), Description: req.GetDescription(), Color: req.GetColor()}, nil
}

func (f *fakeClient) RemoveAssessor(_ context.Context, req *pb.RemoveAssessorRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.recordRemove("assessor", req.Id)
	return &emptypb.Empty{}, nil
}

func (f *fakeClient) PutAssessment(_ context.Context, req *pb.PutAssessmentRequest, _ ...grpc.CallOption) (*pb.Assessment, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	if f.putResult != nil {
		return proto.Clone(f.putResult).(*pb.Assessment), nil
	}
	return &pb.Assessment{Id: 8, ItemId: req.ItemId, AssessorId: req.AssessorId, AssessorName: "claude", TagId: req.TagId, Score: req.Score, Note: req.Note}, nil
}

func (f *fakeClient) RemoveAssessment(_ context.Context, req *pb.RemoveAssessmentRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	return &emptypb.Empty{}, nil
}

func TestAssessors(t *testing.T) {
	fc := &fakeClient{assessors: []*pb.Assessor{
		{Id: 1, Name: "claude", Description: "importance 0-1", Color: "#D97757"},
		{Id: 2, Name: "evil\x00<b>", Description: "a\u202eb", Color: "red;x:url(y)"},
	}}
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/assessors", "", nil)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"name":"claude"`) || !strings.Contains(body, `"id":"1"`) || !strings.Contains(body, "#D97757") {
		t.Errorf("list body: %s", body)
	}
	if strings.Contains(body, "url(y)") || strings.Contains(body, `\u0000`) || strings.Contains(body, "\u202e") {
		t.Errorf("assessor text or color not sanitized: %s", body)
	}

	rec = th.do("POST", "/api/assessors", `{"name":"cvss","description":"CVSS/10","color":"#112233"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	if want := (&pb.AddAssessorRequest{Name: "cvss", Description: "CVSS/10", Color: "#112233"}); !proto.Equal(fc.lastReq, want) {
		t.Errorf("AddAssessorRequest = %v, want %v", fc.lastReq, want)
	}
	if !strings.Contains(rec.Body.String(), `"id":"4"`) {
		t.Errorf("add body: %s", rec.Body)
	}
	if rec := th.do("POST", "/api/assessors", `{"name":"min"}`, nil); rec.Code != http.StatusCreated ||
		!proto.Equal(fc.lastReq, &pb.AddAssessorRequest{Name: "min"}) {
		t.Errorf("minimal add: %d %v", rec.Code, fc.lastReq)
	}

	// PATCH presence: absent = unchanged, "" clears.
	for _, tc := range []struct {
		body string
		want *pb.UpdateAssessorRequest
	}{
		{`{}`, &pb.UpdateAssessorRequest{Id: 7}},
		{`{"name":"new"}`, &pb.UpdateAssessorRequest{Id: 7, Name: ptr("new")}},
		{`{"description":"","color":""}`, &pb.UpdateAssessorRequest{Id: 7, Description: ptr(""), Color: ptr("")}},
	} {
		rec := th.do("PATCH", "/api/assessors/7", tc.body, nil)
		if rec.Code != 200 || !proto.Equal(fc.lastReq, tc.want) {
			t.Errorf("%s: %d, UpdateAssessorRequest = %v, want %v", tc.body, rec.Code, fc.lastReq, tc.want)
		}
	}

	if rec := th.do("DELETE", "/api/assessors/7", `{}`, nil); rec.Code != http.StatusNoContent || len(fc.removed) != 1 || fc.removed[0] != "assessor/7" {
		t.Errorf("remove: %d %v", rec.Code, fc.removed)
	}
}

func TestPutAssessment(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)

	rec := th.do("PUT", "/api/items/12/assessments", `{"assessor":"3","tag":"5","score":0.9,"note":"critical in Cisco IOS"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	want := &pb.PutAssessmentRequest{ItemId: 12, AssessorId: 3, TagId: 5, Score: ptr(0.9), Note: "critical in Cisco IOS"}
	if !proto.Equal(fc.lastReq, want) {
		t.Errorf("PutAssessmentRequest = %v, want %v", fc.lastReq, want)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"item_id":"12"`) || !strings.Contains(body, `"score":0.9`) {
		t.Errorf("put body: %s", body)
	}

	// A score of 0 is a score; a missing score is none; a missing tag is the
	// whole item.
	rec = th.do("PUT", "/api/items/12/assessments", `{"assessor":"3","score":0}`, nil)
	got := fc.lastReq.(*pb.PutAssessmentRequest)
	if rec.Code != 200 || got.Score == nil || *got.Score != 0 || got.TagId != 0 {
		t.Errorf("zero score: %d %v", rec.Code, got)
	}
	rec = th.do("PUT", "/api/items/12/assessments", `{"assessor":"3","note":"n"}`, nil)
	got = fc.lastReq.(*pb.PutAssessmentRequest)
	if rec.Code != 200 || got.Score != nil || got.Note != "n" {
		t.Errorf("note only: %d %v", rec.Code, got)
	}
	// An integer score is fine.
	if rec := th.do("PUT", "/api/items/12/assessments", `{"assessor":"3","score":1}`, nil); rec.Code != 200 || *fc.lastReq.(*pb.PutAssessmentRequest).Score != 1 {
		t.Errorf("integer score: %d", rec.Code)
	}
}

func TestPutAssessmentSanitizesResponse(t *testing.T) {
	fc := &fakeClient{putResult: &pb.Assessment{Id: 1, ItemId: 2, AssessorId: 3, AssessorName: "x\x00y", Note: "<script>alert(1)</script>\u202e\nline\x07two"}}
	th := newTestHandler(t, fc)
	rec := th.do("PUT", "/api/items/2/assessments", `{"assessor":"3","note":"n"}`, nil)
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, `\u0000`) || strings.Contains(body, `\u0007`) || strings.Contains(body, "\u202e") || strings.Contains(body, `\u202e`) {
		t.Errorf("not sanitized: %d %s", rec.Code, body)
	}
	// Markup is left alone (the browser renders text only); newlines stay.
	if !strings.Contains(body, "<script>") && !strings.Contains(body, `<script`) {
		t.Errorf("note text lost: %s", body)
	}
	if !strings.Contains(body, `\n`) {
		t.Errorf("newline dropped: %s", body)
	}
}

func TestRemoveAssessment(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)
	rec := th.do("DELETE", "/api/items/12/assessments?assessor=3&tag=5", "", nil)
	if rec.Code != http.StatusNoContent || !proto.Equal(fc.lastReq, &pb.RemoveAssessmentRequest{ItemId: 12, AssessorId: 3, TagId: 5}) {
		t.Errorf("remove: %d %v", rec.Code, fc.lastReq)
	}
	rec = th.do("DELETE", "/api/items/12/assessments?assessor=3", "", nil)
	if rec.Code != http.StatusNoContent || !proto.Equal(fc.lastReq, &pb.RemoveAssessmentRequest{ItemId: 12, AssessorId: 3}) {
		t.Errorf("remove whole item: %d %v", rec.Code, fc.lastReq)
	}
}

func TestAssessmentBodyValidation(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		code                       int
	}{
		{"no assessor", "PUT", "/api/items/1/assessments", `{"score":0.5}`, 400},
		{"assessor 0", "PUT", "/api/items/1/assessments", `{"assessor":"0","score":0.5}`, 400},
		{"number assessor", "PUT", "/api/items/1/assessments", `{"assessor":3,"score":0.5}`, 400},
		{"name as assessor", "PUT", "/api/items/1/assessments", `{"assessor":"claude","score":0.5}`, 400},
		{"string score", "PUT", "/api/items/1/assessments", `{"assessor":"3","score":"0.5"}`, 400},
		{"null score", "PUT", "/api/items/1/assessments", `{"assessor":"3","score":null}`, 400},
		{"number note", "PUT", "/api/items/1/assessments", `{"assessor":"3","note":5}`, 400},
		{"unknown field", "PUT", "/api/items/1/assessments", `{"assessor":"3","score":0.5,"data":{}}`, 400},
		{"duplicate", "PUT", "/api/items/1/assessments", `{"assessor":"3","score":0.5,"score":0.6}`, 400},
		{"item id in body", "PUT", "/api/items/1/assessments", `{"assessor":"3","score":0.5,"item":"2"}`, 400},
		{"bad item id", "PUT", "/api/items/x/assessments", `{"assessor":"3","score":0.5}`, 400},
		{"zero item id", "PUT", "/api/items/0/assessments", `{"assessor":"3","score":0.5}`, 400},
		{"too large", "PUT", "/api/items/1/assessments", `{"assessor":"3","note":"` + strings.Repeat("a", maxManageBodyBytes) + `"}`, 413},
		{"delete without assessor", "DELETE", "/api/items/1/assessments", ``, 400},
		{"delete bad tag", "DELETE", "/api/items/1/assessments?assessor=3&tag=x", ``, 400},
		{"delete bad assessor", "DELETE", "/api/items/1/assessments?assessor=-3", ``, 400},
		{"assessor unknown field", "POST", "/api/assessors", `{"name":"x","scale":"1"}`, 400},
		{"assessor null", "PATCH", "/api/assessors/1", `{"color":null}`, 400},
		{"assessor bad id", "DELETE", "/api/assessors/0", ``, 400},
		{"no assessor patch route for items", "PATCH", "/api/items/1/assessments", `{}`, 404},
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
		})
	}
}

// Every assessment mutation obeys the CSRF and Host rules: nothing reaches
// the daemon unless the request is JSON from our origin. This is also what an
// HTTP assessor has to satisfy.
func TestAssessmentCSRF(t *testing.T) {
	mutations := []struct{ method, target, body string }{
		{"POST", "/api/assessors", `{"name":"x"}`},
		{"PATCH", "/api/assessors/1", `{"color":"#000000"}`},
		{"DELETE", "/api/assessors/1", `{}`},
		{"PUT", "/api/items/1/assessments", `{"assessor":"3","score":0.5}`},
		{"DELETE", "/api/items/1/assessments?assessor=3", `{}`},
	}
	for _, m := range mutations {
		for _, tc := range []struct {
			name string
			hdr  map[string]string
			code int
		}{
			{"cross-site origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
			{"no origin headers", map[string]string{"Origin": ""}, http.StatusForbidden},
			{"form content type", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
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
	}
}

func TestAssessmentRPCErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{status.Error(codes.InvalidArgument, "score must be between 0 and 1, got 2"), http.StatusBadRequest},
		{status.Error(codes.AlreadyExists, `assessor "claude" already exists`), http.StatusConflict},
		{status.Error(codes.NotFound, "item 12 not found"), http.StatusNotFound},
		{errUnavailable, http.StatusServiceUnavailable},
	} {
		fc := &fakeClient{err: tc.err}
		th := newTestHandler(t, fc)
		for _, req := range []struct{ method, target, body string }{
			{"PUT", "/api/items/12/assessments", `{"assessor":"3","score":2}`},
			{"DELETE", "/api/items/12/assessments?assessor=3", ``},
			{"POST", "/api/assessors", `{"name":"claude"}`},
			{"DELETE", "/api/assessors/3", ``},
		} {
			rec := th.do(req.method, req.target, req.body, nil)
			if rec.Code != tc.code {
				t.Errorf("%v %s %s: status %d, want %d", tc.err, req.method, req.target, rec.Code, tc.code)
			}
			if msg := decode(t, rec)["error"]; msg != status.Convert(tc.err).Message() {
				t.Errorf("%v: error %q", tc.err, msg)
			}
		}
	}
}

func TestItemsAssessmentFilters(t *testing.T) {
	fc := &fakeClient{search: &pb.SearchResponse{Items: []*pb.Item{{Id: 1, Title: "t", Assessments: []*pb.Assessment{
		{Id: 1, AssessorId: 3, AssessorName: "claude\x00", Score: ptr(0.8), Note: "n\x07"},
	}}}}}
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/items?assessor=3&min_score=0.7&unassessed=4&sort=score&tag=5", "", nil)
	if rec.Code != 200 {
		t.Fatalf("items: %d %s", rec.Code, rec.Body)
	}
	got := fc.lastSrch
	if got.AssessorId != 3 || got.MinScore == nil || *got.MinScore != 0.7 || got.UnassessedBy != 4 || got.Sort != "score" || got.TagId != 5 {
		t.Errorf("SearchRequest = %v", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"assessments"`) || !strings.Contains(body, `"score":0.8`) || strings.Contains(body, `\u0000`) || strings.Contains(body, `\u0007`) {
		t.Errorf("items body: %s", body)
	}

	// min_score=0 is a minimum, not "unset".
	if rec := th.do("GET", "/api/items?assessor=3&min_score=0", "", nil); rec.Code != 200 || fc.lastSrch.MinScore == nil || *fc.lastSrch.MinScore != 0 {
		t.Errorf("min_score=0: %d %v", rec.Code, fc.lastSrch)
	}
	if rec := th.do("GET", "/api/items?assessor=3", "", nil); rec.Code != 200 || fc.lastSrch.MinScore != nil {
		t.Errorf("no min_score: %d %v", rec.Code, fc.lastSrch)
	}
	for _, q := range []string{"min_score=x", "min_score=NaN", "min_score=Inf", "min_score=1.5", "min_score=-0.1", "assessor=x", "assessor=-1", "unassessed=x"} {
		if rec := th.do("GET", "/api/items?"+q, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, rec.Code)
		}
	}

	// The daemon's rule that min_score needs an assessor comes back as 400.
	fc.err = status.Error(codes.InvalidArgument, "min_score and sort score need an assessor_id")
	if rec := th.do("GET", "/api/items?min_score=0.5", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("min_score without assessor: %d", rec.Code)
	}
}

func TestStreamAssessmentFilterAndUpdateEvent(t *testing.T) {
	fs := newFakeStream(16)
	fc := &fakeClient{stream: fs}
	srv := startStreamServer(t, fc, Config{PingInterval: time.Minute})

	fs.msgs <- completeMsg
	resp, _ := getStream(t, srv, "?assessor=3&min_score=0.5&unassessed=4&sort=score")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	f := fs.filter()
	if f == nil || f.AssessorId != 3 || f.MinScore == nil || *f.MinScore != 0.5 || f.UnassessedBy != 4 || f.Sort != "score" {
		t.Errorf("filter sent to daemon = %v", f)
	}

	sc := bufio.NewScanner(resp.Body)
	if ev := readEvents(t, sc, 2); len(ev) != 2 || ev[0].event != "reset" || ev[1].event != "complete" {
		t.Fatalf("initial events = %+v", ev)
	}

	upd := func(matches bool) *pb.ServerMessage {
		return &pb.ServerMessage{
			Msg: &pb.ServerMessage_ItemUpdate{ItemUpdate: &pb.Item{Id: 7, Title: "t", Link: "javascript:alert(1)", Assessments: []*pb.Assessment{
				{Id: 1, ItemId: 7, AssessorId: 3, AssessorName: "claude", Score: ptr(0.9), Note: "bad\x00note"},
			}}},
			UpdateMatches: matches,
		}
	}
	fs.msgs <- upd(true)
	fs.msgs <- upd(false)
	ev := readEvents(t, sc, 2)
	if len(ev) != 2 {
		t.Fatalf("update events = %+v", ev)
	}
	for i, wantMatches := range []bool{true, false} {
		if ev[i].event != "update" {
			t.Fatalf("event %d = %+v, want update", i, ev[i])
		}
		var d struct {
			Matches bool            `json:"matches"`
			Item    json.RawMessage `json:"item"`
		}
		if err := json.Unmarshal([]byte(ev[i].data), &d); err != nil {
			t.Fatalf("update data %q: %v", ev[i].data, err)
		}
		if d.Matches != wantMatches {
			t.Errorf("update %d matches = %v, want %v", i, d.Matches, wantMatches)
		}
		item := string(d.Item)
		if !strings.Contains(item, `"id":"7"`) || !strings.Contains(item, `"score":0.9`) {
			t.Errorf("update item = %s", item)
		}
		if strings.Contains(item, "javascript:") || strings.Contains(item, `\u0000`) {
			t.Errorf("update item not sanitized: %s", item)
		}
	}
}

func TestViewAssessmentFilter(t *testing.T) {
	zero := 0.0
	fc := &fakeClient{views: []*pb.SavedView{
		{Id: 1, Name: "important", Filter: &pb.ViewFilter{Sort: "score", AssessorId: 3, MinScore: ptr(0.7), UnassessedBy: 4}},
		{Id: 2, Name: "zero", Filter: &pb.ViewFilter{AssessorId: 3, MinScore: &zero}},
	}}
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/views", "", nil)
	want := `{"views":[{"id":"1","name":"important","filter":{"sort":"score","assessor":"3","min_score":0.7,"unassessed":"4"}},` +
		`{"id":"2","name":"zero","filter":{"assessor":"3","min_score":0}}]}`
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("list views:\n got %s\nwant %s", rec.Body, want)
	}

	rec = th.do("POST", "/api/views", `{"name":"v","filter":{"sort":"score","assessor":"3","min_score":0.7,"unassessed":"4"}}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	wantReq := &pb.AddSavedViewRequest{Name: "v", Filter: &pb.ViewFilter{Sort: "score", AssessorId: 3, MinScore: ptr(0.7), UnassessedBy: 4}}
	if !proto.Equal(fc.lastReq, wantReq) {
		t.Errorf("AddSavedViewRequest = %v, want %v", fc.lastReq, wantReq)
	}
	// min_score 0 keeps its presence through the API.
	th.do("PATCH", "/api/views/1", `{"filter":{"assessor":"3","min_score":0}}`, nil)
	if f := fc.lastReq.(*pb.UpdateSavedViewRequest).Filter; f.MinScore == nil || *f.MinScore != 0 || f.AssessorId != 3 {
		t.Errorf("min_score 0 lost: %v", f)
	}

	for _, body := range []string{
		`{"name":"v","filter":{"assessor":3}}`,
		`{"name":"v","filter":{"min_score":"0.5"}}`,
		`{"name":"v","filter":{"min_score":null}}`,
		`{"name":"v","filter":{"unassessed":"x"}}`,
		`{"name":"v","filter":{"assessors":"3"}}`,
	} {
		fc.lastReq = nil
		if rec := th.do("POST", "/api/views", body, nil); rec.Code != http.StatusBadRequest || fc.lastReq != nil {
			t.Errorf("%s: %d, reached the daemon: %v", body, rec.Code, fc.lastReq)
		}
	}
}

func TestSafeText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain text", "plain text"},
		{"line one\nline two\tcol", "line one\nline two\tcol"},
		{"<b>markup</b> stays as text", "<b>markup</b> stays as text"},
		{"nul\x00and bell\x07gone", "nuland bellgone"},
		{"bidi \u202eoverride\u2069", "bidi override"},
		{"bad \xff utf8", "bad � utf8"},
		{"emoji ✓ é", "emoji ✓ é"},
		{"", ""},
	} {
		if got := safeText(tc.in); got != tc.want {
			t.Errorf("safeText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
