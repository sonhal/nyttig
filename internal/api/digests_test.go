package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func (f *fakeClient) ListDigestSeries(_ context.Context, req *pb.ListDigestSeriesRequest, _ ...grpc.CallOption) (*pb.ListDigestSeriesResponse, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	return &pb.ListDigestSeriesResponse{Series: f.series}, nil
}

func (f *fakeClient) AddDigestSeries(_ context.Context, req *pb.AddDigestSeriesRequest, _ ...grpc.CallOption) (*pb.DigestSeries, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	return &pb.DigestSeries{Id: 6, AssessorId: req.AssessorId, AssessorName: "claude", Name: req.Name, Description: req.Description}, nil
}

func (f *fakeClient) UpdateDigestSeries(_ context.Context, req *pb.UpdateDigestSeriesRequest, _ ...grpc.CallOption) (*pb.DigestSeries, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	return &pb.DigestSeries{Id: req.Id, Name: req.GetName(), Description: req.GetDescription()}, nil
}

func (f *fakeClient) RemoveDigestSeries(_ context.Context, req *pb.RemoveDigestSeriesRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.recordRemove("digest-series", req.Id)
	return &emptypb.Empty{}, nil
}

func (f *fakeClient) ReorderDigestSeries(_ context.Context, req *pb.ReorderDigestSeriesRequest, _ ...grpc.CallOption) (*pb.ListDigestSeriesResponse, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	return &pb.ListDigestSeriesResponse{Series: f.series}, nil
}

func (f *fakeClient) AddDigest(_ context.Context, req *pb.AddDigestRequest, _ ...grpc.CallOption) (*pb.Digest, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	if f.digest != nil {
		return proto.Clone(f.digest).(*pb.Digest), nil
	}
	return &pb.Digest{Id: 9, SeriesId: req.SeriesId, Title: req.Title, Body: req.Body, PeriodStart: req.PeriodStart, PeriodEnd: req.PeriodEnd}, nil
}

func (f *fakeClient) UpdateDigest(_ context.Context, req *pb.UpdateDigestRequest, _ ...grpc.CallOption) (*pb.Digest, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	return &pb.Digest{Id: req.Id, Title: req.GetTitle(), Body: req.GetBody()}, nil
}

func (f *fakeClient) RemoveDigest(_ context.Context, req *pb.RemoveDigestRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.recordRemove("digest", req.Id)
	return &emptypb.Empty{}, nil
}

func (f *fakeClient) GetDigest(_ context.Context, req *pb.GetDigestRequest, _ ...grpc.CallOption) (*pb.Digest, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	if f.digest != nil {
		return proto.Clone(f.digest).(*pb.Digest), nil
	}
	return &pb.Digest{Id: req.Id, Title: "t"}, nil
}

func (f *fakeClient) ListDigests(_ context.Context, req *pb.ListDigestsRequest, _ ...grpc.CallOption) (*pb.ListDigestsResponse, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	resp := &pb.ListDigestsResponse{HasMore: true}
	if f.digest != nil {
		resp.Digests = []*pb.Digest{proto.Clone(f.digest).(*pb.Digest)}
	}
	return resp, nil
}

func ts(day int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC))
}

func TestDigestSeries(t *testing.T) {
	fc := &fakeClient{series: []*pb.DigestSeries{
		{Id: 1, AssessorId: 2, AssessorName: "claude", Name: "daily-cve", Description: "CVE news", DigestCount: 3, LatestPeriodEnd: ts(5)},
		{Id: 2, AssessorId: 2, AssessorName: "ev\x00il", Name: "a\u202eb\x07", Description: "x\x1b[2J"},
	}}
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/digest-series?assessor=2", "", nil)
	if rec.Code != 200 || !proto.Equal(fc.lastReq, &pb.ListDigestSeriesRequest{AssessorId: 2}) {
		t.Fatalf("list: %d %s (%v)", rec.Code, rec.Body, fc.lastReq)
	}
	body := rec.Body.String()
	for _, want := range []string{`"series":[`, `"id":"1"`, `"assessor_id":"2"`, `"assessor_name":"claude"`, `"digest_count":3`, `"latest_period_end":"2026-10-05T00:00:00Z"`} {
		if !strings.Contains(body, want) {
			t.Errorf("list body lacks %s: %s", want, body)
		}
	}
	for _, bad := range []string{`\u0000`, `\u0007`, `\u001b`, "\u202e", `\u202e`} {
		if strings.Contains(body, bad) {
			t.Errorf("series text not sanitized (%s): %s", bad, body)
		}
	}
	if rec := th.do("GET", "/api/digest-series", "", nil); rec.Code != 200 || !proto.Equal(fc.lastReq, &pb.ListDigestSeriesRequest{}) {
		t.Errorf("list all: %d %v", rec.Code, fc.lastReq)
	}
	if rec := th.do("GET", "/api/digest-series?assessor=x", "", nil); rec.Code != 400 {
		t.Errorf("bad assessor: %d", rec.Code)
	}

	rec = th.do("POST", "/api/digest-series", `{"assessor":"2","name":"monthly","description":"Month in review"}`, nil)
	if rec.Code != http.StatusCreated || !proto.Equal(fc.lastReq, &pb.AddDigestSeriesRequest{AssessorId: 2, Name: "monthly", Description: "Month in review"}) {
		t.Errorf("add: %d %s (%v)", rec.Code, rec.Body, fc.lastReq)
	}
	if rec := th.do("POST", "/api/digest-series", `{"assessor":"2","name":"min"}`, nil); rec.Code != http.StatusCreated ||
		!proto.Equal(fc.lastReq, &pb.AddDigestSeriesRequest{AssessorId: 2, Name: "min"}) {
		t.Errorf("minimal add: %d %v", rec.Code, fc.lastReq)
	}

	// PATCH presence: absent = unchanged, "" clears.
	for _, tc := range []struct {
		body string
		want *pb.UpdateDigestSeriesRequest
	}{
		{`{}`, &pb.UpdateDigestSeriesRequest{Id: 7}},
		{`{"name":"new"}`, &pb.UpdateDigestSeriesRequest{Id: 7, Name: ptr("new")}},
		{`{"description":""}`, &pb.UpdateDigestSeriesRequest{Id: 7, Description: ptr("")}},
	} {
		rec := th.do("PATCH", "/api/digest-series/7", tc.body, nil)
		if rec.Code != 200 || !proto.Equal(fc.lastReq, tc.want) {
			t.Errorf("%s: %d, request = %v, want %v", tc.body, rec.Code, fc.lastReq, tc.want)
		}
	}

	rec = th.do("PUT", "/api/digest-series/order", `{"ids":["3","1","2"]}`, nil)
	if rec.Code != 200 || !proto.Equal(fc.lastReq, &pb.ReorderDigestSeriesRequest{Ids: []int64{3, 1, 2}}) || !strings.Contains(rec.Body.String(), `"series"`) {
		t.Errorf("reorder: %d %s (%v)", rec.Code, rec.Body, fc.lastReq)
	}

	if rec := th.do("DELETE", "/api/digest-series/7", `{}`, nil); rec.Code != http.StatusNoContent || len(fc.removed) != 1 || fc.removed[0] != "digest-series/7" {
		t.Errorf("remove: %d %v", rec.Code, fc.removed)
	}
}

func TestDigestRoundTrip(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)

	rec := th.do("POST", "/api/digests", `{"series":"3","title":"CVE news","body":"# Hi\n\nSee [#12]","period_start":"2026-10-05T00:00:00Z","period_end":"2026-10-05T23:59:59+02:00","items":["12","15"],"inputs":["8"]}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	want := &pb.AddDigestRequest{
		SeriesId: 3, Title: "CVE news", Body: "# Hi\n\nSee [#12]",
		PeriodStart: ts(5), PeriodEnd: timestamppb.New(time.Date(2026, 10, 5, 21, 59, 59, 0, time.UTC)),
		ItemIds: []int64{12, 15}, InputIds: []int64{8},
	}
	if !proto.Equal(fc.lastReq, want) {
		t.Errorf("AddDigestRequest = %v, want %v", fc.lastReq, want)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"id":"9"`) || !strings.Contains(body, `"series_id":"3"`) || !strings.Contains(body, `"period_start":"2026-10-05T00:00:00Z"`) {
		t.Errorf("add body: %s", body)
	}

	// Items and inputs are optional.
	if rec := th.do("POST", "/api/digests", `{"series":"3","title":"t","body":"b","period_start":"2026-10-05T00:00:00Z","period_end":"2026-10-05T00:00:00Z"}`, nil); rec.Code != http.StatusCreated ||
		len(fc.lastReq.(*pb.AddDigestRequest).ItemIds) != 0 {
		t.Errorf("minimal add: %d %v", rec.Code, fc.lastReq)
	}

	// PATCH presence: absent = unchanged, [] = none, no series field.
	for _, tc := range []struct {
		body string
		want *pb.UpdateDigestRequest
	}{
		{`{}`, &pb.UpdateDigestRequest{Id: 7}},
		{`{"title":"new","body":"b2"}`, &pb.UpdateDigestRequest{Id: 7, Title: ptr("new"), Body: ptr("b2")}},
		{`{"period_end":"2026-10-06T00:00:00Z"}`, &pb.UpdateDigestRequest{Id: 7, PeriodEnd: ts(6)}},
		{`{"items":[],"inputs":["4"]}`, &pb.UpdateDigestRequest{Id: 7, ItemIds: &pb.IDList{Ids: []int64{}}, InputIds: &pb.IDList{Ids: []int64{4}}}},
	} {
		rec := th.do("PATCH", "/api/digests/7", tc.body, nil)
		if rec.Code != 200 || !proto.Equal(fc.lastReq, tc.want) {
			t.Errorf("%s: %d, request = %v, want %v", tc.body, rec.Code, fc.lastReq, tc.want)
		}
	}
	// An empty items array stays present on the wire.
	th.do("PATCH", "/api/digests/7", `{"items":[]}`, nil)
	if got := fc.lastReq.(*pb.UpdateDigestRequest); got.ItemIds == nil || got.InputIds != nil {
		t.Errorf("empty items lost its presence: %v", got)
	}

	if rec := th.do("GET", "/api/digests/7", "", nil); rec.Code != 200 || !proto.Equal(fc.lastReq, &pb.GetDigestRequest{Id: 7}) {
		t.Errorf("get: %d %v", rec.Code, fc.lastReq)
	}
	if rec := th.do("DELETE", "/api/digests/7", `{}`, nil); rec.Code != http.StatusNoContent || len(fc.removed) != 1 || fc.removed[0] != "digest/7" {
		t.Errorf("remove: %d %v", rec.Code, fc.removed)
	}
}

func TestListDigestsParams(t *testing.T) {
	fc := &fakeClient{digest: &pb.Digest{Id: 1, Title: "t", Body: "b"}}
	th := newTestHandler(t, fc)
	for _, tc := range []struct {
		target string
		want   *pb.ListDigestsRequest
	}{
		{"/api/digests?series=3", &pb.ListDigestsRequest{SeriesId: 3, Limit: 20}},
		{"/api/digests?series=3&before=9&limit=5&body=1", &pb.ListDigestsRequest{SeriesId: 3, BeforeId: 9, Limit: 5, IncludeBody: true}},
		{"/api/digests?series=3&limit=100&body=0", &pb.ListDigestsRequest{SeriesId: 3, Limit: 100}},
	} {
		rec := th.do("GET", tc.target, "", nil)
		if rec.Code != 200 || !proto.Equal(fc.lastReq, tc.want) {
			t.Errorf("%s: %d, request = %v, want %v", tc.target, rec.Code, fc.lastReq, tc.want)
		}
		if !strings.Contains(rec.Body.String(), `"has_more":true`) {
			t.Errorf("%s: body %s", tc.target, rec.Body)
		}
	}
	for _, target := range []string{
		"/api/digests", "/api/digests?series=0", "/api/digests?series=x", "/api/digests?series=3&before=-1",
		"/api/digests?series=3&limit=0", "/api/digests?series=3&limit=101", "/api/digests?series=3&body=yes",
	} {
		fc.lastReq = nil
		if rec := th.do("GET", target, "", nil); rec.Code != 400 || fc.lastReq != nil {
			t.Errorf("%s: %d (daemon called: %v)", target, rec.Code, fc.lastReq != nil)
		}
	}
}

func TestDigestSanitizesResponse(t *testing.T) {
	fc := &fakeClient{digest: &pb.Digest{
		Id: 1, SeriesName: "s\x00", AssessorName: "a\u202e", Title: "T\x1b[31m", Body: "<script>alert(1)</script>\u202e\nline\x07two\ttab\r",
		Items: []*pb.DigestItem{
			{ItemId: 1, Title: "i\x00", Link: "javascript:alert(1)", SourceName: "src\x07"},
			{ItemId: 2, Title: "ok", Link: "https://example.com/a", SourceName: "src"},
		},
		Inputs: []*pb.DigestRef{{Id: 3, Title: "o\x1b", SeriesName: "m\x00"}},
	}}
	th := newTestHandler(t, fc)
	for _, target := range []string{"/api/digests/1", "/api/digests?series=1&body=1"} {
		body := th.do("GET", target, "", nil).Body.String()
		for _, bad := range []string{`\u0000`, `\u0007`, `\u001b`, `\r`, "\u202e", `\u202e`, "javascript:"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: %q survived: %s", target, bad, body)
			}
		}
		// Markup stays text for the renderer; newline and tab stay.
		for _, want := range []string{"<script>alert(1)</script>", `\n`, `\t`, "https://example.com/a"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: lost %q: %s", target, want, body)
			}
		}
	}
	rec := th.do("POST", "/api/digests", `{"series":"1","title":"t","body":"b","period_start":"2026-10-05T00:00:00Z","period_end":"2026-10-05T00:00:00Z"}`, nil)
	if strings.Contains(rec.Body.String(), `\u0000`) || strings.Contains(rec.Body.String(), "javascript:") {
		t.Errorf("add response not sanitized: %s", rec.Body)
	}
}

func TestDigestBodyValidation(t *testing.T) {
	const good = `"series":"1","title":"t","body":"b","period_start":"2026-10-05T00:00:00Z","period_end":"2026-10-05T00:00:00Z"`
	for _, tc := range []struct {
		name, method, target, body string
		code                       int
	}{
		{"no series", "POST", "/api/digests", `{"title":"t","body":"b"}`, 400},
		{"series 0", "POST", "/api/digests", `{"series":"0","title":"t"}`, 400},
		{"number series", "POST", "/api/digests", `{"series":1,"title":"t"}`, 400},
		{"unknown field", "POST", "/api/digests", `{` + good + `,"extra":1}`, 400},
		{"duplicate field", "POST", "/api/digests", `{` + good + `,"title":"u"}`, 400},
		{"null body", "POST", "/api/digests", `{"series":"1","body":null}`, 400},
		{"number title", "POST", "/api/digests", `{"series":"1","title":5}`, 400},
		{"bad period", "POST", "/api/digests", `{"series":"1","period_start":"yesterday"}`, 400},
		{"date only period", "POST", "/api/digests", `{"series":"1","period_start":"2026-10-05"}`, 400},
		{"number period", "POST", "/api/digests", `{"series":"1","period_end":1790000000}`, 400},
		{"numeric item ids", "POST", "/api/digests", `{"series":"1","items":[1,2]}`, 400},
		{"zero item id", "POST", "/api/digests", `{"series":"1","items":["0"]}`, 400},
		{"items not an array", "POST", "/api/digests", `{"series":"1","items":"1"}`, 400},
		{"bad input id", "POST", "/api/digests", `{"series":"1","inputs":["x"]}`, 400},
		{"series in patch", "PATCH", "/api/digests/1", `{"series":"2"}`, 400},
		{"null in patch", "PATCH", "/api/digests/1", `{"title":null}`, 400},
		{"bad id", "PATCH", "/api/digests/x", `{}`, 400},
		{"zero id", "GET", "/api/digests/0", ``, 400},
		{"delete bad id", "DELETE", "/api/digests/-1", ``, 400},
		{"too large", "POST", "/api/digests", `{"series":"1","body":"` + strings.Repeat("a", maxDigestRequestBytes) + `"}`, 413},
		{"series no assessor", "POST", "/api/digest-series", `{"name":"x"}`, 400},
		{"series assessor 0", "POST", "/api/digest-series", `{"assessor":"0","name":"x"}`, 400},
		{"series unknown field", "POST", "/api/digest-series", `{"assessor":"1","name":"x","position":1}`, 400},
		{"series patch assessor", "PATCH", "/api/digest-series/1", `{"assessor":"2"}`, 400},
		{"series null", "PATCH", "/api/digest-series/1", `{"name":null}`, 400},
		{"series bad id", "DELETE", "/api/digest-series/0", ``, 400},
		{"order missing", "PUT", "/api/digest-series/order", `{}`, 400},
		{"order numbers", "PUT", "/api/digest-series/order", `{"ids":[1,2]}`, 400},
		{"order zero", "PUT", "/api/digest-series/order", `{"ids":["0"]}`, 400},
		{"order extra", "PUT", "/api/digest-series/order", `{"ids":["1"],"x":1}`, 400},
		{"put digest", "PUT", "/api/digests/1", `{}`, 404},
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

	// A body of the daemon's limit fits: 64 KiB of text, escaped.
	fc := &fakeClient{}
	th := newTestHandler(t, fc)
	big := strings.Repeat("a\n", 32*1024)
	rec := th.do("POST", "/api/digests", `{"series":"1","title":"t","period_start":"2026-10-05T00:00:00Z","period_end":"2026-10-05T00:00:00Z","body":"`+strings.ReplaceAll(big, "\n", `\n`)+`"}`, nil)
	if rec.Code != http.StatusCreated || len(fc.lastReq.(*pb.AddDigestRequest).Body) != 64*1024 {
		t.Errorf("64 KiB body: %d %.100s", rec.Code, rec.Body)
	}
}

func TestDigestCSRF(t *testing.T) {
	mutations := []struct{ method, target, body string }{
		{"POST", "/api/digest-series", `{"assessor":"1","name":"x"}`},
		{"PATCH", "/api/digest-series/1", `{"name":"y"}`},
		{"PUT", "/api/digest-series/order", `{"ids":["1"]}`},
		{"DELETE", "/api/digest-series/1", `{}`},
		{"POST", "/api/digests", `{"series":"1","title":"t","body":"b"}`},
		{"PATCH", "/api/digests/1", `{"title":"t"}`},
		{"DELETE", "/api/digests/1", `{}`},
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

func TestDigestRPCErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{status.Error(codes.InvalidArgument, "body is required"), http.StatusBadRequest},
		{status.Error(codes.AlreadyExists, `series "x" already exists`), http.StatusConflict},
		{status.Error(codes.NotFound, "digest 12 not found"), http.StatusNotFound},
		{errUnavailable, http.StatusServiceUnavailable},
	} {
		fc := &fakeClient{err: tc.err}
		th := newTestHandler(t, fc)
		for _, req := range []struct{ method, target, body string }{
			{"GET", "/api/digest-series", ``},
			{"POST", "/api/digest-series", `{"assessor":"1","name":"x"}`},
			{"PATCH", "/api/digest-series/1", `{"name":"x"}`},
			{"DELETE", "/api/digest-series/1", ``},
			{"PUT", "/api/digest-series/order", `{"ids":["1"]}`},
			{"GET", "/api/digests?series=1", ``},
			{"GET", "/api/digests/1", ``},
			{"POST", "/api/digests", `{"series":"1","title":"t","body":"b"}`},
			{"PATCH", "/api/digests/1", `{"title":"t"}`},
			{"DELETE", "/api/digests/1", ``},
		} {
			if rec := th.do(req.method, req.target, req.body, nil); rec.Code != tc.code {
				t.Errorf("%s %s with %v: status %d, want %d", req.method, req.target, tc.err, rec.Code, tc.code)
			}
		}
	}
}
