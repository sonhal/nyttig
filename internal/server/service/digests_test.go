package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func ts(d int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC))
}

func addSeries(t *testing.T, svc *Service, assessor int64, name string) *pb.DigestSeries {
	t.Helper()
	s, err := svc.AddDigestSeries(context.Background(), &pb.AddDigestSeriesRequest{AssessorId: assessor, Name: name})
	if err != nil {
		t.Fatalf("AddDigestSeries(%q): %v", name, err)
	}
	return s
}

func addDigest(t *testing.T, svc *Service, series int64, title string, end int, items, inputs []int64) *pb.Digest {
	t.Helper()
	d, err := svc.AddDigest(context.Background(), &pb.AddDigestRequest{
		SeriesId: series, Title: title, Body: "body of " + title,
		PeriodStart: ts(end - 1), PeriodEnd: ts(end), ItemIds: items, InputIds: inputs,
	})
	if err != nil {
		t.Fatalf("AddDigest(%q): %v", title, err)
	}
	return d
}

func TestValidateDigestBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"plain", "# Title\n\n- one\n- two\t(tab)", true},
		{"unicode", "héllo ✓ 日本語", true},
		{"at the limit", strings.Repeat("a", maxDigestBodyBytes), true},
		{"over the limit", strings.Repeat("a", maxDigestBodyBytes+1), false},
		{"multibyte over the limit", strings.Repeat("é", maxDigestBodyBytes/2+1), false},
		{"empty", "", false},
		{"blank", " \n\t ", false},
		{"invalid utf-8", "bad \xff", false},
		{"escape", "red \x1b[31mtext", false},
		{"nul", "a\x00b", false},
		{"carriage return", "a\r\nb", false},
		{"c1 control", "a\u0085b", false},
	} {
		if err := validateDigestBody(tc.body); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestValidateDigestIDs(t *testing.T) {
	got, err := validateDigestIDs("item_ids", []int64{3, 1, 3, 2, 1}, 3)
	if err != nil || len(got) != 3 || got[0] != 3 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("dedup = %v, %v", got, err)
	}
	for name, ids := range map[string][]int64{"zero": {0}, "negative": {1, -2}} {
		if _, err := validateDigestIDs("item_ids", ids, 10); err == nil {
			t.Errorf("%s id accepted", name)
		}
	}
	over := make([]int64, 4)
	for i := range over {
		over[i] = int64(i + 1)
	}
	if _, err := validateDigestIDs("item_ids", over, 3); err == nil {
		t.Error("too many ids accepted")
	}
}

func TestAddDigestSeries(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	claude := addAssessor(t, svc, "claude")
	gpt := addAssessor(t, svc, "gpt")

	s, err := svc.AddDigestSeries(ctx, &pb.AddDigestSeriesRequest{AssessorId: claude.Id, Name: "daily-cve", Description: "CVE news"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Id == 0 || s.AssessorName != "claude" || s.Name != "daily-cve" || s.Description != "CVE news" ||
		s.DigestCount != 0 || s.LatestPeriodEnd != nil || s.CreatedAt == nil {
		t.Fatalf("got %v", s)
	}

	_, err = svc.AddDigestSeries(ctx, &pb.AddDigestSeriesRequest{AssessorId: claude.Id, Name: "DAILY-CVE"})
	wantCode(t, err, codes.AlreadyExists)
	if _, err := svc.AddDigestSeries(ctx, &pb.AddDigestSeriesRequest{AssessorId: gpt.Id, Name: "daily-cve"}); err != nil {
		t.Errorf("same name for another assessor: %v", err)
	}
	_, err = svc.AddDigestSeries(ctx, &pb.AddDigestSeriesRequest{AssessorId: 999, Name: "x"})
	wantCode(t, err, codes.NotFound)

	for name, req := range map[string]*pb.AddDigestSeriesRequest{
		"no assessor":   {Name: "x"},
		"empty name":    {AssessorId: claude.Id, Name: " "},
		"long name":     {AssessorId: claude.Id, Name: strings.Repeat("a", maxSeriesNameLen+1)},
		"control name":  {AssessorId: claude.Id, Name: "a\x1bb"},
		"long desc":     {AssessorId: claude.Id, Name: "y", Description: strings.Repeat("d", maxAssessorDescLen+1)},
		"control desc":  {AssessorId: claude.Id, Name: "y", Description: "a\nb"},
		"negative user": {AssessorId: -1, Name: "y"},
	} {
		_, err := svc.AddDigestSeries(ctx, req)
		if got := status.Code(err); got != codes.InvalidArgument {
			t.Errorf("%s: code = %v (%v), want InvalidArgument", name, got, err)
		}
	}
}

func TestUpdateRemoveReorderDigestSeries(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	claude := addAssessor(t, svc, "claude")
	a := addSeries(t, svc, claude.Id, "a")
	b := addSeries(t, svc, claude.Id, "b")
	c := addSeries(t, svc, claude.Id, "c")

	got, err := svc.UpdateDigestSeries(ctx, &pb.UpdateDigestSeriesRequest{Id: a.Id, Description: proto.String("about a")})
	if err != nil || got.Name != "a" || got.Description != "about a" {
		t.Fatalf("description only: %v, %v", got, err)
	}
	got, err = svc.UpdateDigestSeries(ctx, &pb.UpdateDigestSeriesRequest{Id: a.Id, Name: proto.String("renamed"), Description: proto.String("")})
	if err != nil || got.Name != "renamed" || got.Description != "" {
		t.Fatalf("rename and clear: %v, %v", got, err)
	}
	_, err = svc.UpdateDigestSeries(ctx, &pb.UpdateDigestSeriesRequest{Id: a.Id, Name: proto.String("B")})
	wantCode(t, err, codes.AlreadyExists)
	_, err = svc.UpdateDigestSeries(ctx, &pb.UpdateDigestSeriesRequest{Id: a.Id, Name: proto.String("")})
	wantCode(t, err, codes.InvalidArgument)
	_, err = svc.UpdateDigestSeries(ctx, &pb.UpdateDigestSeriesRequest{Id: 999, Name: proto.String("x")})
	wantCode(t, err, codes.NotFound)

	resp, err := svc.ReorderDigestSeries(ctx, &pb.ReorderDigestSeriesRequest{Ids: []int64{c.Id, a.Id, b.Id}})
	if err != nil || len(resp.Series) != 3 || resp.Series[0].Id != c.Id || resp.Series[2].Id != b.Id {
		t.Fatalf("reorder = %v, %v", resp, err)
	}
	_, err = svc.ReorderDigestSeries(ctx, &pb.ReorderDigestSeriesRequest{Ids: []int64{a.Id}})
	wantCode(t, err, codes.InvalidArgument)

	list, err := svc.ListDigestSeries(ctx, &pb.ListDigestSeriesRequest{})
	if err != nil || len(list.Series) != 3 {
		t.Fatalf("list = %v, %v", list, err)
	}
	_, err = svc.ListDigestSeries(ctx, &pb.ListDigestSeriesRequest{AssessorId: -1})
	wantCode(t, err, codes.InvalidArgument)

	if _, err := svc.RemoveDigestSeries(ctx, &pb.RemoveDigestSeriesRequest{Id: b.Id}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.RemoveDigestSeries(ctx, &pb.RemoveDigestSeriesRequest{Id: b.Id})
	wantCode(t, err, codes.NotFound)
}

func TestAddDigest_Validation(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	claude := addAssessor(t, svc, "claude")
	series := addSeries(t, svc, claude.Id, "daily")
	src := addSource(t, svc, "feed", "https://example.com/feed.xml")
	item := insertItem(t, database, src.Id, "one", "", 0)
	first := addDigest(t, svc, series.Id, "first", 4, nil, nil)

	good := func() *pb.AddDigestRequest {
		return &pb.AddDigestRequest{SeriesId: series.Id, Title: "t", Body: "b", PeriodStart: ts(1), PeriodEnd: ts(2)}
	}
	many := func(n int) []int64 {
		ids := make([]int64, n)
		for i := range ids {
			ids[i] = int64(i + 1)
		}
		return ids
	}

	invalid := map[string]func(*pb.AddDigestRequest){
		"no series":         func(r *pb.AddDigestRequest) { r.SeriesId = 0 },
		"empty title":       func(r *pb.AddDigestRequest) { r.Title = "  " },
		"long title":        func(r *pb.AddDigestRequest) { r.Title = strings.Repeat("t", maxDigestTitleLen+1) },
		"control in title":  func(r *pb.AddDigestRequest) { r.Title = "a\nb" },
		"empty body":        func(r *pb.AddDigestRequest) { r.Body = "" },
		"huge body":         func(r *pb.AddDigestRequest) { r.Body = strings.Repeat("x", maxDigestBodyBytes+1) },
		"bad utf-8 body":    func(r *pb.AddDigestRequest) { r.Body = "\xff" },
		"escape in body":    func(r *pb.AddDigestRequest) { r.Body = "\x1b]0;x\x07" },
		"no start":          func(r *pb.AddDigestRequest) { r.PeriodStart = nil },
		"no end":            func(r *pb.AddDigestRequest) { r.PeriodEnd = nil },
		"end before start":  func(r *pb.AddDigestRequest) { r.PeriodStart, r.PeriodEnd = ts(3), ts(2) },
		"out of range time": func(r *pb.AddDigestRequest) { r.PeriodEnd = &timestamppb.Timestamp{Seconds: 1 << 60} },
		"too many items":    func(r *pb.AddDigestRequest) { r.ItemIds = many(maxDigestItems + 1) },
		"too many inputs":   func(r *pb.AddDigestRequest) { r.InputIds = many(maxDigestInputs + 1) },
		"zero item id":      func(r *pb.AddDigestRequest) { r.ItemIds = []int64{0} },
		"negative input id": func(r *pb.AddDigestRequest) { r.InputIds = []int64{-1} },
	}
	for name, mutate := range invalid {
		req := good()
		mutate(req)
		_, err := svc.AddDigest(ctx, req)
		if got := status.Code(err); got != codes.InvalidArgument {
			t.Errorf("%s: code = %v (%v), want InvalidArgument", name, got, err)
		}
	}

	// Limits are inclusive and duplicates do not count.
	req := good()
	req.Body = strings.Repeat("é", maxDigestBodyBytes/2)
	if _, err := svc.AddDigest(ctx, req); err != nil {
		t.Errorf("body at the limit: %v", err)
	}
	req = good()
	req.InputIds = []int64{first.Id, first.Id, first.Id}
	if d, err := svc.AddDigest(ctx, req); err != nil || len(d.Inputs) != 1 {
		t.Errorf("duplicate inputs: %v, %v", d, err)
	}
	req = good()
	req.PeriodStart, req.PeriodEnd = ts(2), ts(2)
	if _, err := svc.AddDigest(ctx, req); err != nil {
		t.Errorf("equal period ends: %v", err)
	}

	// NotFound names the first missing id.
	req = good()
	req.SeriesId = 999
	_, err := svc.AddDigest(ctx, req)
	wantCode(t, err, codes.NotFound)
	req = good()
	req.ItemIds = []int64{item, 777, 888}
	_, err = svc.AddDigest(ctx, req)
	wantCode(t, err, codes.NotFound)
	if !strings.Contains(err.Error(), "777") {
		t.Errorf("error %q does not name item 777", err)
	}
	req = good()
	req.InputIds = []int64{first.Id, 555}
	_, err = svc.AddDigest(ctx, req)
	wantCode(t, err, codes.NotFound)
	if !strings.Contains(err.Error(), "555") {
		t.Errorf("error %q does not name digest 555", err)
	}
}

func TestAddDigest_StoresAndReturnsEverything(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	claude := addAssessor(t, svc, "claude")
	series := addSeries(t, svc, claude.Id, "daily")
	src := addSource(t, svc, "feed", "https://example.com/feed.xml")
	i1 := insertItem(t, database, src.Id, "one", "", 0)
	i2 := insertItem(t, database, src.Id, "two", "", 5)

	first := addDigest(t, svc, series.Id, "first", 4, []int64{i1}, nil)
	// Periods are normalized to UTC with whole seconds.
	zone := time.FixedZone("x", 3600)
	start := time.Date(2026, 10, 5, 1, 0, 0, 999, zone)
	d, err := svc.AddDigest(ctx, &pb.AddDigestRequest{
		SeriesId: series.Id, Title: "second", Body: "see [#1]",
		PeriodStart: timestamppb.New(start), PeriodEnd: ts(6), ItemIds: []int64{i1, i2}, InputIds: []int64{first.Id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.SeriesName != "daily" || d.AssessorName != "claude" || d.AssessorId != claude.Id || d.Body != "see [#1]" {
		t.Fatalf("got %v", d)
	}
	if got := d.PeriodStart.AsTime(); !got.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("period_start = %v", got)
	}
	if len(d.Items) != 2 || d.Items[0].Title != "two" || d.Items[1].Title != "one" || d.Items[0].SourceName != "feed" ||
		d.Items[1].Link != "https://example.com/one" || d.Items[1].Published == nil {
		t.Errorf("items = %v", d.Items)
	}
	if len(d.Inputs) != 1 || d.Inputs[0].Id != first.Id || d.Inputs[0].SeriesName != "daily" {
		t.Errorf("inputs = %v", d.Inputs)
	}

	// Always creates: the same request again is another digest.
	again, err := svc.AddDigest(ctx, &pb.AddDigestRequest{SeriesId: series.Id, Title: "second", Body: "see [#1]", PeriodStart: ts(5), PeriodEnd: ts(6)})
	if err != nil || again.Id == d.Id {
		t.Fatalf("second add = %v, %v", again, err)
	}

	// GetDigest and ListDigests.
	got, err := svc.GetDigest(ctx, &pb.GetDigestRequest{Id: d.Id})
	if err != nil || got.Body != "see [#1]" || len(got.Items) != 2 {
		t.Fatalf("GetDigest = %v, %v", got, err)
	}
	_, err = svc.GetDigest(ctx, &pb.GetDigestRequest{Id: 999})
	wantCode(t, err, codes.NotFound)

	list, err := svc.ListDigests(ctx, &pb.ListDigestsRequest{SeriesId: series.Id, Limit: 2})
	if err != nil || len(list.Digests) != 2 || !list.HasMore || list.Digests[0].Id != again.Id || list.Digests[0].Body != "" {
		t.Fatalf("ListDigests = %v, %v", list, err)
	}
	rest, err := svc.ListDigests(ctx, &pb.ListDigestsRequest{SeriesId: series.Id, BeforeId: list.Digests[1].Id, IncludeBody: true})
	if err != nil || len(rest.Digests) != 1 || rest.HasMore || rest.Digests[0].Id != first.Id || rest.Digests[0].Body == "" {
		t.Fatalf("second page = %v, %v", rest, err)
	}
	for name, req := range map[string]*pb.ListDigestsRequest{
		"no series":       {},
		"negative before": {SeriesId: series.Id, BeforeId: -1},
		"negative limit":  {SeriesId: series.Id, Limit: -1},
	} {
		_, err := svc.ListDigests(ctx, req)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err = svc.ListDigests(ctx, &pb.ListDigestsRequest{SeriesId: 999})
	wantCode(t, err, codes.NotFound)

	// The series list carries the count and the latest period.
	sl, _ := svc.ListDigestSeries(ctx, &pb.ListDigestSeriesRequest{AssessorId: claude.Id})
	if sl.Series[0].DigestCount != 3 || !sl.Series[0].LatestPeriodEnd.AsTime().Equal(ts(6).AsTime()) {
		t.Errorf("series = %v", sl.Series[0])
	}
}

func TestUpdateDigest(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	claude := addAssessor(t, svc, "claude")
	series := addSeries(t, svc, claude.Id, "daily")
	src := addSource(t, svc, "feed", "https://example.com/feed.xml")
	i1 := insertItem(t, database, src.Id, "one", "", 0)
	i2 := insertItem(t, database, src.Id, "two", "", 5)
	older := addDigest(t, svc, series.Id, "older", 3, nil, nil)
	d := addDigest(t, svc, series.Id, "d", 5, []int64{i1}, []int64{older.Id}) // period 4..5

	// Only what is set changes; unset link sets are kept.
	got, err := svc.UpdateDigest(ctx, &pb.UpdateDigestRequest{Id: d.Id, Title: proto.String("new"), ItemIds: &pb.IDList{Ids: []int64{i2}}})
	if err != nil || got.Title != "new" || got.Body != "body of d" || len(got.Items) != 1 || got.Items[0].ItemId != i2 || len(got.Inputs) != 1 {
		t.Fatalf("update = %v, %v", got, err)
	}
	// An empty list clears.
	got, err = svc.UpdateDigest(ctx, &pb.UpdateDigestRequest{Id: d.Id, ItemIds: &pb.IDList{}, InputIds: &pb.IDList{}})
	if err != nil || len(got.Items) != 0 || len(got.Inputs) != 0 {
		t.Fatalf("clear = %v, %v", got, err)
	}
	got, err = svc.UpdateDigest(ctx, &pb.UpdateDigestRequest{Id: d.Id, Body: proto.String("new body"), PeriodEnd: ts(9)})
	if err != nil || got.Body != "new body" || !got.PeriodEnd.AsTime().Equal(ts(9).AsTime()) || !got.PeriodStart.AsTime().Equal(ts(4).AsTime()) {
		t.Fatalf("body/period = %v, %v", got, err)
	}

	for name, req := range map[string]*pb.UpdateDigestRequest{
		"empty title":        {Id: d.Id, Title: proto.String("")},
		"empty body":         {Id: d.Id, Body: proto.String("")},
		"escape in body":     {Id: d.Id, Body: proto.String("\x1b[2J")},
		"end before stored":  {Id: d.Id, PeriodEnd: ts(2)},
		"start after stored": {Id: d.Id, PeriodStart: ts(20)},
		"both reversed":      {Id: d.Id, PeriodStart: ts(8), PeriodEnd: ts(7)},
		"own input":          {Id: d.Id, InputIds: &pb.IDList{Ids: []int64{older.Id, d.Id}}},
		"zero item":          {Id: d.Id, ItemIds: &pb.IDList{Ids: []int64{0}}},
		"too many inputs": {Id: d.Id, InputIds: &pb.IDList{Ids: func() []int64 {
			l := make([]int64, maxDigestInputs+1)
			for i := range l {
				l[i] = int64(i + 1)
			}
			return l
		}()}},
	} {
		_, err := svc.UpdateDigest(ctx, req)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: code = %v (%v), want InvalidArgument", name, status.Code(err), err)
		}
	}
	_, err = svc.UpdateDigest(ctx, &pb.UpdateDigestRequest{Id: d.Id, ItemIds: &pb.IDList{Ids: []int64{i1, 999}}})
	wantCode(t, err, codes.NotFound)
	_, err = svc.UpdateDigest(ctx, &pb.UpdateDigestRequest{Id: d.Id, InputIds: &pb.IDList{Ids: []int64{999}}})
	wantCode(t, err, codes.NotFound)
	_, err = svc.UpdateDigest(ctx, &pb.UpdateDigestRequest{Id: 999, Title: proto.String("x")})
	wantCode(t, err, codes.NotFound)

	// A rejected update changes nothing.
	after, _ := svc.GetDigest(ctx, &pb.GetDigestRequest{Id: d.Id})
	if after.Body != "new body" || len(after.Items) != 0 {
		t.Errorf("a rejected update changed the digest: %v", after)
	}
}

func TestRemoveDigest(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	claude := addAssessor(t, svc, "claude")
	series := addSeries(t, svc, claude.Id, "daily")
	a := addDigest(t, svc, series.Id, "a", 3, nil, nil)
	b := addDigest(t, svc, series.Id, "b", 4, nil, []int64{a.Id})

	if _, err := svc.RemoveDigest(ctx, &pb.RemoveDigestRequest{Id: a.Id}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.RemoveDigest(ctx, &pb.RemoveDigestRequest{Id: a.Id})
	wantCode(t, err, codes.NotFound)
	got, err := svc.GetDigest(ctx, &pb.GetDigestRequest{Id: b.Id})
	if err != nil || len(got.Inputs) != 0 {
		t.Fatalf("b after removing its input: %v, %v", got, err)
	}

	// Removing the assessor takes the series and the digests.
	if _, err := svc.RemoveAssessor(ctx, &pb.RemoveAssessorRequest{Id: claude.Id}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.GetDigest(ctx, &pb.GetDigestRequest{Id: b.Id})
	wantCode(t, err, codes.NotFound)
}

// Like TestSourceColorSurvivesWire: the generated code must carry the digest
// fields, including the periods, the links and the presence of the optional
// and wrapped fields of the update.
func TestDigestSurvivesWire(t *testing.T) {
	in := &pb.Digest{
		Id: 1, SeriesId: 2, SeriesName: "daily", AssessorId: 3, AssessorName: "claude",
		Title: "t", Body: "# b", PeriodStart: ts(1), PeriodEnd: ts(2), CreatedAt: ts(3), UpdatedAt: ts(4),
		Items:  []*pb.DigestItem{{ItemId: 5, Title: "i", Link: "https://example.com", SourceName: "s", Published: ts(1)}},
		Inputs: []*pb.DigestRef{{Id: 6, Title: "r", SeriesName: "monthly", PeriodEnd: ts(2)}},
	}
	series := &pb.DigestSeries{Id: 1, AssessorId: 2, AssessorName: "a", Name: "n", Description: "d", Position: 3,
		CreatedAt: ts(1), DigestCount: 4, LatestPeriodEnd: ts(2)}
	add := &pb.AddDigestRequest{SeriesId: 1, Title: "t", Body: "b", PeriodStart: ts(1), PeriodEnd: ts(2), ItemIds: []int64{1, 2}, InputIds: []int64{3}}
	list := &pb.ListDigestsResponse{Digests: []*pb.Digest{in}, HasMore: true}
	for name, msg := range map[string]proto.Message{"digest": in, "series": series, "add": add, "list": list,
		"list request":  &pb.ListDigestsRequest{SeriesId: 1, BeforeId: 2, Limit: 3, IncludeBody: true},
		"series update": &pb.UpdateDigestSeriesRequest{Id: 1, Name: proto.String(""), Description: proto.String("")},
	} {
		raw, err := proto.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		back := msg.ProtoReflect().New().Interface()
		if err := proto.Unmarshal(raw, back); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(msg, back) {
			t.Errorf("%s: after round trip %v, want %v", name, back, msg)
		}
	}

	up := &pb.UpdateDigestRequest{Id: 1, Title: proto.String(""), Body: proto.String(""), ItemIds: &pb.IDList{}, InputIds: &pb.IDList{Ids: []int64{2}}}
	raw, _ := proto.Marshal(up)
	got := &pb.UpdateDigestRequest{}
	if err := proto.Unmarshal(raw, got); err != nil {
		t.Fatal(err)
	}
	if got.Title == nil || got.Body == nil || got.ItemIds == nil || got.InputIds == nil || got.PeriodStart != nil || got.PeriodEnd != nil {
		t.Fatalf("presence lost on the wire: %v", got)
	}
	raw, _ = proto.Marshal(&pb.UpdateDigestRequest{Id: 1})
	got = &pb.UpdateDigestRequest{}
	_ = proto.Unmarshal(raw, got)
	if got.Title != nil || got.Body != nil || got.ItemIds != nil || got.InputIds != nil {
		t.Fatalf("unset fields appeared on the wire: %v", got)
	}
}
