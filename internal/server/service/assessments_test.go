package service

import (
	"context"
	"math"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func addAssessor(t *testing.T, svc *Service, name string) *pb.Assessor {
	t.Helper()
	a, err := svc.AddAssessor(context.Background(), &pb.AddAssessorRequest{Name: name})
	if err != nil {
		t.Fatalf("AddAssessor(%q): %v", name, err)
	}
	return a
}

func TestValidateScore(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    float64
		ok   bool
	}{
		{"zero", 0, true},
		{"one", 1, true},
		{"middle", 0.37, true},
		{"just below zero", -0.0001, false},
		{"just above one", 1.0001, false},
		{"NaN", math.NaN(), false},
		{"+Inf", math.Inf(1), false},
		{"-Inf", math.Inf(-1), false},
	} {
		err := validateScore("score", tc.s)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestValidateAssessmentFields(t *testing.T) {
	if err := validateNote(strings.Repeat("a", maxNoteBytes)); err != nil {
		t.Errorf("note at the limit: %v", err)
	}
	if err := validateNote(strings.Repeat("a", maxNoteBytes+1)); err == nil {
		t.Error("note over the limit accepted")
	}
	if err := validateNote("bad \xff utf8"); err == nil {
		t.Error("invalid UTF-8 note accepted")
	}
	if err := validateNote("line one\nline two\t(multi-line is fine)"); err != nil {
		t.Errorf("multi-line note: %v", err)
	}
	if err := validateAssessorDescription(strings.Repeat("é", maxAssessorDescLen)); err != nil {
		t.Errorf("description at the limit: %v", err)
	}
	for name, d := range map[string]string{
		"long":    strings.Repeat("a", maxAssessorDescLen+1),
		"control": "a\nb",
	} {
		if err := validateAssessorDescription(d); err == nil {
			t.Errorf("%s description accepted", name)
		}
	}

	min := func(f float64) *float64 { return &f }
	for _, tc := range []struct {
		name    string
		assess  int64
		min     *float64
		unassd  int64
		sort    string
		wantErr bool
	}{
		{"nothing", 0, nil, 0, "", false},
		{"assessor alone", 1, nil, 0, "", false},
		{"min score", 1, min(0.5), 0, "", false},
		{"min score zero", 1, min(0), 0, "", false},
		{"sort score", 1, nil, 0, "score", false},
		{"unassessed alone", 0, nil, 2, "", false},
		{"min score no assessor", 0, min(0.5), 0, "", true},
		{"sort score no assessor", 0, nil, 0, "score", true},
		{"min score NaN", 1, min(math.NaN()), 0, "", true},
		{"min score +Inf", 1, min(math.Inf(1)), 0, "", true},
		{"min score above 1", 1, min(1.5), 0, "", true},
		{"min score negative", 1, min(-0.1), 0, "", true},
		{"negative assessor", -1, nil, 0, "", true},
		{"negative unassessed", 0, nil, -1, "", true},
	} {
		err := validateAssessmentFilter(tc.assess, tc.min, tc.unassd, tc.sort)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestAssessor_CRUD(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	a, err := svc.AddAssessor(ctx, &pb.AddAssessorRequest{Name: "claude", Description: "importance 0-1", Color: "#112233"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Id == 0 || a.Name != "claude" || a.Description != "importance 0-1" || a.Color != "#112233" || a.CreatedAt == nil {
		t.Fatalf("added: %v", a)
	}
	addAssessor(t, svc, "cvss")

	list, err := svc.ListAssessors(ctx, &emptypb.Empty{})
	if err != nil || len(list.Assessors) != 2 || list.Assessors[0].Name != "claude" {
		t.Fatalf("ListAssessors = %v, %v", list, err)
	}

	// Patch: unset = unchanged, "" clears.
	up, err := svc.UpdateAssessor(ctx, &pb.UpdateAssessorRequest{Id: a.Id, Name: proto.String("claude-2"), Color: proto.String("")})
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != "claude-2" || up.Color != "" || up.Description != "importance 0-1" {
		t.Fatalf("updated: %v", up)
	}

	if _, err := svc.RemoveAssessor(ctx, &pb.RemoveAssessorRequest{Id: a.Id}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.RemoveAssessor(ctx, &pb.RemoveAssessorRequest{Id: a.Id})
	wantCode(t, err, codes.NotFound)
}

func TestAssessor_ValidationAndErrors(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	addAssessor(t, svc, "claude")
	other := addAssessor(t, svc, "other")

	for name, req := range map[string]*pb.AddAssessorRequest{
		"empty name":      {Name: " "},
		"long name":       {Name: strings.Repeat("n", maxAssessorNameLen+1)},
		"control in name": {Name: "a\tb"},
		"bad color":       {Name: "x", Color: "red"},
		"long desc":       {Name: "x", Description: strings.Repeat("d", maxAssessorDescLen+1)},
		"control in desc": {Name: "x", Description: "a\x00b"},
	} {
		_, err := svc.AddAssessor(ctx, req)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, codes.InvalidArgument)
	}

	_, err := svc.AddAssessor(ctx, &pb.AddAssessorRequest{Name: "claude"})
	wantCode(t, err, codes.AlreadyExists)
	_, err = svc.UpdateAssessor(ctx, &pb.UpdateAssessorRequest{Id: other.Id, Name: proto.String("claude")})
	wantCode(t, err, codes.AlreadyExists)
	_, err = svc.UpdateAssessor(ctx, &pb.UpdateAssessorRequest{Id: 999, Name: proto.String("x")})
	wantCode(t, err, codes.NotFound)
	_, err = svc.UpdateAssessor(ctx, &pb.UpdateAssessorRequest{Id: other.Id, Color: proto.String("nope")})
	wantCode(t, err, codes.InvalidArgument)
}

func TestPutAssessment_ValidationTable(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "feed", "https://example.com/feed.xml")
	item := insertItem(t, database, src.Id, "title", "desc", 0)
	claude := addAssessor(t, svc, "claude")
	tag, err := svc.AddTag(ctx, &pb.AddTagRequest{Name: "CVE"})
	if err != nil {
		t.Fatal(err)
	}
	f := func(v float64) *float64 { return &v }

	for _, tc := range []struct {
		name string
		req  *pb.PutAssessmentRequest
		want codes.Code
	}{
		{"score only", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: f(0.5)}, codes.OK},
		{"zero score", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: f(0)}, codes.OK},
		{"note only", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Note: "n"}, codes.OK},
		{"with tag", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, TagId: tag.Id, Score: f(1)}, codes.OK},
		{"neither", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id}, codes.InvalidArgument},
		{"NaN", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: f(math.NaN())}, codes.InvalidArgument},
		{"+Inf", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: f(math.Inf(1))}, codes.InvalidArgument},
		{"-Inf", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: f(math.Inf(-1))}, codes.InvalidArgument},
		{"above one", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: f(1.1)}, codes.InvalidArgument},
		{"negative", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: f(-0.1)}, codes.InvalidArgument},
		{"note too long", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Note: strings.Repeat("a", maxNoteBytes+1)}, codes.InvalidArgument},
		{"note not UTF-8", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Note: "\xff"}, codes.InvalidArgument},
		{"no item id", &pb.PutAssessmentRequest{AssessorId: claude.Id, Score: f(0.5)}, codes.InvalidArgument},
		{"no assessor id", &pb.PutAssessmentRequest{ItemId: item, Score: f(0.5)}, codes.InvalidArgument},
		{"negative tag", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, TagId: -1, Score: f(0.5)}, codes.InvalidArgument},
		{"unknown item", &pb.PutAssessmentRequest{ItemId: 999, AssessorId: claude.Id, Score: f(0.5)}, codes.NotFound},
		{"unknown assessor", &pb.PutAssessmentRequest{ItemId: item, AssessorId: 999, Score: f(0.5)}, codes.NotFound},
		{"unknown tag", &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, TagId: 999, Score: f(0.5)}, codes.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.PutAssessment(ctx, tc.req)
			wantCode(t, err, tc.want)
		})
	}
}

func TestPutAssessment_ReplacesAndShowsOnItems(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "feed", "https://example.com/feed.xml")
	item := insertItem(t, database, src.Id, "title", "desc", 0)
	claude := addAssessor(t, svc, "claude")
	half := 0.5

	first, err := svc.PutAssessment(ctx, &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: &half, Note: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if first.AssessorName != "claude" || first.Score == nil || *first.Score != 0.5 || first.UpdatedAt == nil {
		t.Fatalf("first = %v", first)
	}
	// Replacing with a note only drops the score.
	second, err := svc.PutAssessment(ctx, &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Note: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Id != first.Id || second.Score != nil || second.Note != "new" {
		t.Fatalf("second = %v", second)
	}

	res, err := svc.Search(ctx, &pb.SearchRequest{})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("Search = %v, %v", res, err)
	}
	as := res.Items[0].Assessments
	if len(as) != 1 || as[0].Note != "new" || as[0].Score != nil {
		t.Fatalf("item assessments = %v", as)
	}

	if _, err := svc.RemoveAssessment(ctx, &pb.RemoveAssessmentRequest{ItemId: item, AssessorId: claude.Id}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.RemoveAssessment(ctx, &pb.RemoveAssessmentRequest{ItemId: item, AssessorId: claude.Id})
	wantCode(t, err, codes.NotFound)
	_, err = svc.RemoveAssessment(ctx, &pb.RemoveAssessmentRequest{AssessorId: claude.Id})
	wantCode(t, err, codes.InvalidArgument)
}

func TestSearch_AssessmentFilters(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "feed", "https://example.com/feed.xml")
	a := insertItem(t, database, src.Id, "A", "x", 3)
	b := insertItem(t, database, src.Id, "B", "x", 2)
	insertItem(t, database, src.Id, "C", "x", 1)
	claude := addAssessor(t, svc, "claude")
	f := func(v float64) *float64 { return &v }
	for item, score := range map[int64]float64{a: 0.2, b: 0.9} {
		if _, err := svc.PutAssessment(ctx, &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: f(score)}); err != nil {
			t.Fatal(err)
		}
	}

	got := func(req *pb.SearchRequest) []string {
		t.Helper()
		res, err := svc.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search(%v): %v", req, err)
		}
		return titles(res.Items)
	}
	if g := got(&pb.SearchRequest{AssessorId: claude.Id, MinScore: f(0.5)}); strings.Join(g, "") != "B" {
		t.Errorf("min_score: %v", g)
	}
	if g := got(&pb.SearchRequest{AssessorId: claude.Id, Sort: "score"}); strings.Join(g, "") != "BAC" {
		t.Errorf("sort score: %v", g)
	}
	if g := got(&pb.SearchRequest{UnassessedBy: claude.Id}); strings.Join(g, "") != "C" {
		t.Errorf("unassessed_by: %v", g)
	}
	if g := got(&pb.SearchRequest{AssessorId: claude.Id}); strings.Join(g, "") != "ABC" {
		t.Errorf("assessor_id alone must not change the order: %v", g)
	}
	for name, req := range map[string]*pb.SearchRequest{
		"min_score without assessor":  {MinScore: f(0.5)},
		"sort score without assessor": {Sort: "score"},
		"NaN":                         {AssessorId: claude.Id, MinScore: f(math.NaN())},
		"bad sort":                    {Sort: "random"},
	} {
		_, err := svc.Search(ctx, req)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, codes.InvalidArgument)
	}
}

func TestSavedViews_AssessorFilter(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	claude := addAssessor(t, svc, "claude")
	cvss := addAssessor(t, svc, "cvss")
	min := 0.7

	v, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "important", Filter: &pb.ViewFilter{
		AssessorId: claude.Id, MinScore: &min, Sort: "score", UnassessedBy: cvss.Id,
	}})
	if err != nil {
		t.Fatal(err)
	}
	f := v.Filter
	if f.AssessorId != claude.Id || f.MinScore == nil || *f.MinScore != 0.7 || f.Sort != "score" || f.UnassessedBy != cvss.Id {
		t.Fatalf("filter = %v", f)
	}

	// Replacing the filter with an empty one clears the assessor fields.
	up, err := svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: v.Id, Filter: &pb.ViewFilter{}})
	if err != nil {
		t.Fatal(err)
	}
	if up.Filter.AssessorId != 0 || up.Filter.MinScore != nil || up.Filter.UnassessedBy != 0 {
		t.Fatalf("after clear: %v", up.Filter)
	}

	for _, req := range map[string]*pb.ViewFilter{
		"unknown assessor":     {AssessorId: 999},
		"unknown unassessed":   {UnassessedBy: 999},
		"unknown, with score":  {AssessorId: 999, Sort: "score"},
		"unknown, min":         {AssessorId: 999, MinScore: &min},
		"unknown unassessed 2": {AssessorId: claude.Id, UnassessedBy: 999},
	} {
		_, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "x", Filter: req})
		wantCode(t, err, codes.NotFound)
	}
	for name, req := range map[string]*pb.ViewFilter{
		"min without assessor": {MinScore: &min},
		"score without":        {Sort: "score"},
		"NaN":                  {AssessorId: claude.Id, MinScore: proto.Float64(math.NaN())},
		"negative assessor":    {AssessorId: -1},
		"negative unassessed":  {UnassessedBy: -1},
		"min above one":        {AssessorId: claude.Id, MinScore: proto.Float64(2)},
	} {
		_, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "x", Filter: req})
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, codes.InvalidArgument)
	}

	// Deleting the assessor keeps the view and drops the fields.
	v2, _ := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "again", Filter: &pb.ViewFilter{
		Search: "q", AssessorId: claude.Id, MinScore: &min, Sort: "score",
	}})
	if _, err := svc.RemoveAssessor(ctx, &pb.RemoveAssessorRequest{Id: claude.Id}); err != nil {
		t.Fatal(err)
	}
	list, _ := svc.ListSavedViews(ctx, &emptypb.Empty{})
	for _, sv := range list.Views {
		if sv.Id == v2.Id && (sv.Filter.AssessorId != 0 || sv.Filter.MinScore != nil || sv.Filter.Sort != "newest" || sv.Filter.Search != "q") {
			t.Errorf("view after assessor delete: %v", sv.Filter)
		}
	}
}

// Like TestSourceColorSurvivesWire: the generated code must carry the new
// fields, and a score of 0 must stay different from no score.
func TestAssessmentSurvivesWire(t *testing.T) {
	zero, half := 0.0, 0.5
	in := &pb.Item{Id: 1, Assessments: []*pb.Assessment{
		{Id: 1, ItemId: 1, AssessorId: 2, AssessorName: "claude", TagId: 3, Score: &zero, Note: "n"},
		{Id: 2, ItemId: 1, AssessorId: 2, AssessorName: "claude", Note: "no score"},
		{Id: 3, ItemId: 1, AssessorId: 4, AssessorName: "cvss", Score: &half},
	}}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out := &pb.Item{}
	if err := proto.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(in, out) {
		t.Fatalf("after round trip %v, want %v", out, in)
	}
	if s := out.Assessments[0].Score; s == nil || *s != 0 {
		t.Errorf("a score of 0 lost its presence: %v", s)
	}
	if out.Assessments[1].Score != nil {
		t.Errorf("no score became %v", *out.Assessments[1].Score)
	}

	// Filters on all three messages keep min_score's presence too.
	for name, msg := range map[string]proto.Message{
		"search": &pb.SearchRequest{AssessorId: 1, MinScore: &zero, UnassessedBy: 2},
		"stream": &pb.StreamFilter{AssessorId: 1, MinScore: &zero, UnassessedBy: 2},
		"view":   &pb.ViewFilter{AssessorId: 1, MinScore: &zero, UnassessedBy: 2},
	} {
		raw, _ := proto.Marshal(msg)
		back := msg.ProtoReflect().New().Interface()
		if err := proto.Unmarshal(raw, back); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(msg, back) {
			t.Errorf("%s: after round trip %v, want %v", name, back, msg)
		}
	}
	empty := &pb.SearchRequest{}
	raw, _ = proto.Marshal(empty)
	back := &pb.SearchRequest{}
	_ = proto.Unmarshal(raw, back)
	if back.MinScore != nil {
		t.Errorf("unset min_score appeared on the wire: %v", *back.MinScore)
	}

	up := &pb.UpdateAssessorRequest{Id: 1, Description: proto.String("")}
	raw, _ = proto.Marshal(up)
	got := &pb.UpdateAssessorRequest{}
	_ = proto.Unmarshal(raw, got)
	if got.Description == nil || got.Name != nil || got.Color != nil {
		t.Errorf("UpdateAssessorRequest presence: %v", got)
	}
}
