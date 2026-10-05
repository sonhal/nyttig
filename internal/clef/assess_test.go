package clef

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func f(v float64) *float64 { return &v }

func levels(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "L" + string(rune('0'+i))
	}
	return out
}

func noulQuestion(tag int64) BoundQuestion {
	return BoundQuestion{Spec: QuestionSpec{Type: TypeNoul, Instructions: "x"}, TagID: tag}
}

func scoreQuestion(tag int64, n int) BoundQuestion {
	return BoundQuestion{Spec: QuestionSpec{Type: TypeScore, Instructions: "x", Levels: levels(n)}, TagID: tag}
}

func TestBoundQuestion_IDAndWire(t *testing.T) {
	if id := noulQuestion(0).ID(); id != "item" {
		t.Errorf("id = %q", id)
	}
	q := scoreQuestion(42, 3)
	if q.ID() != "tag.42" {
		t.Errorf("id = %q", q.ID())
	}
	b, _ := json.Marshal(q.Wire())
	if string(b) != `{"type":"score","instructions":"x","criteria":["L0","L1","L2"]}` {
		t.Errorf("wire = %s", b)
	}
	n := BoundQuestion{Spec: QuestionSpec{Type: TypeNoul, Instructions: "x", Noul: &NoulCriteria{True: "a", False: "b"}}}
	b, _ = json.Marshal(n.Wire())
	if string(b) != `{"type":"noul","instructions":"x","criteria":{"true":"a","false":"b"}}` {
		t.Errorf("wire = %s", b)
	}
	b, _ = json.Marshal(noulQuestion(0).Wire())
	if strings.Contains(string(b), "criteria") {
		t.Errorf("wire = %s", b)
	}
}

func TestResolveQuestions(t *testing.T) {
	tags := []*pb.Tag{{Id: 1, Name: "CVE"}, {Id: 2, Name: "linux security"}}
	got, err := ResolveQuestions([]QuestionSpec{{Type: TypeNoul}, {Tag: "cve", Type: TypeNoul}}, tags)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].TagID != 0 || got[1].TagID != 1 {
		t.Errorf("tag ids = %d, %d", got[0].TagID, got[1].TagID)
	}
	if _, err := ResolveQuestions([]QuestionSpec{{Tag: "nope"}}, tags); err == nil {
		t.Error("an unknown tag must fail")
	}
}

func TestApplicable(t *testing.T) {
	// 1 CVE; 2 linux-cve (child of CVE); 3 kernel; 4 kernel-cve (parents 2 and 3); 5 unrelated.
	tags := []*pb.Tag{
		{Id: 1, Name: "CVE"},
		{Id: 2, Name: "linux-cve", ParentIds: []int64{1}},
		{Id: 3, Name: "kernel"},
		{Id: 4, Name: "kernel-cve", ParentIds: []int64{2, 3}},
		{Id: 5, Name: "other"},
	}
	tree := NewTagTree(tags)
	qs := []BoundQuestion{noulQuestion(0), scoreQuestion(1, 5), noulQuestion(3)}
	item := func(ids ...int64) *pb.Item {
		it := &pb.Item{}
		for _, id := range ids {
			it.Tags = append(it.Tags, &pb.Tag{Id: id})
		}
		return it
	}
	tests := []struct {
		name string
		item *pb.Item
		want []string
	}{
		{"no tags: whole-item only", item(), []string{"item"}},
		{"the tag itself", item(1), []string{"item", "tag.1"}},
		{"a child tag", item(2), []string{"item", "tag.1"}},
		{"a grandchild with two parents reaches both", item(4), []string{"item", "tag.1", "tag.3"}},
		{"second parent only", item(3), []string{"item", "tag.3"}},
		{"unrelated tag", item(5), []string{"item"}},
		{"an unknown tag id", item(99), []string{"item"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, q := range Applicable(tc.item, qs, tree) {
				got = append(got, q.ID())
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplicable_Cycle(t *testing.T) {
	tree := NewTagTree([]*pb.Tag{{Id: 1, ParentIds: []int64{2}}, {Id: 2, ParentIds: []int64{1}}})
	got := Applicable(&pb.Item{Tags: []*pb.Tag{{Id: 1}}}, []BoundQuestion{noulQuestion(2)}, tree)
	if len(got) != 1 {
		t.Errorf("got %d questions", len(got))
	}
}

func TestBuildState(t *testing.T) {
	item := &pb.Item{
		Title: "T", SourceName: "Src", Link: "https://x/y", Description: "hello world",
		Published: timestamppb.New(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)),
		Tags:      []*pb.Tag{{Id: 1, Name: "CVE"}, {Id: 2, Name: "linux"}},
	}
	b, _ := json.Marshal(BuildState(item, 5))
	want := `{"link":"https://x/y","published":"2026-10-05T12:00:00Z","source":"Src","tags":["CVE","linux"],"text":"hello","title":"T"}`
	if string(b) != want {
		t.Errorf("state = %s\nwant    %s", b, want)
	}
	b, _ = json.Marshal(BuildState(&pb.Item{Title: "only"}, 100))
	if string(b) != `{"title":"only"}` {
		t.Errorf("empty fields must be left out: %s", b)
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abc", 3, "abc"},
		{"abc", 2, "ab"},
		{"abc", 0, ""},
		{"æøå日本語", 3, "æøå"},
		{"日本語日本語", 4, "日本語日"},
		{"emoji 😀😀😀", 8, "emoji 😀😀"},
		{"", 3, ""},
	}
	for _, tc := range tests {
		got := TruncateRunes(tc.in, tc.n)
		if got != tc.want || !utf8.ValidString(got) {
			t.Errorf("TruncateRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestToAssessments_ScoreMapping(t *testing.T) {
	tests := []struct {
		name      string
		levels    int
		raw       float64
		want      float64
		wantLevel string
	}{
		{"2 levels low edge", 2, 0, 0, "L0"},
		{"2 levels high edge", 2, 1, 1, "L1"},
		{"2 levels between", 2, 0.25, 0.25, "L0"},
		{"10 levels low edge", 10, 0, 0, "L0"},
		{"10 levels high edge", 10, 9, 1, "L9"},
		{"10 levels between", 10, 4.5, 0.5, "L5"}, // no probabilities: round half away from zero
		{"5 levels 3.2", 5, 3.2, 0.8, "L3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := scoreQuestion(7, tc.levels)
			resp := &Response{Answers: map[string]Answer{"tag.7": {Type: TypeScore, Score: f(tc.raw), Confidence: f(0.714)}}}
			reqs, err := ToAssessments(&pb.Item{Id: 11}, 3, []BoundQuestion{q}, resp)
			if err != nil {
				t.Fatal(err)
			}
			r := reqs[0]
			if r.ItemId != 11 || r.AssessorId != 3 || r.TagId != 7 {
				t.Errorf("request = %+v", r)
			}
			if math.Abs(*r.Score-tc.want) > 1e-9 {
				t.Errorf("score = %v, want %v", *r.Score, tc.want)
			}
			if !strings.HasPrefix(r.Note, tc.wantLevel+" (") || !strings.HasSuffix(r.Note, ", confidence 0.71") {
				t.Errorf("note = %q", r.Note)
			}
		})
	}
}

func TestToAssessments_Note(t *testing.T) {
	q := scoreQuestion(1, 5)
	q.Spec.Levels = []string{"None", "Low", "Medium", "High", "Critical"}
	probs := json.RawMessage(`[0,0.05,0.15,0.7,0.1]`)
	resp := &Response{Answers: map[string]Answer{"tag.1": {Type: TypeScore, Score: f(3.2), Confidence: f(0.71), Probabilities: probs}}}
	reqs, err := ToAssessments(&pb.Item{Id: 1}, 1, []BoundQuestion{q}, resp)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := reqs[0].Note, "High (3.2/4), confidence 0.71"; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	// The most likely level can differ from the rounded score.
	resp.Answers["tag.1"] = Answer{Type: TypeScore, Score: f(2.4), Probabilities: json.RawMessage(`[0,0,0.3,0.1,0.6]`)}
	reqs, _ = ToAssessments(&pb.Item{Id: 1}, 1, []BoundQuestion{q}, resp)
	if got, want := reqs[0].Note, "Critical (2.4/4)"; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	// Unusable probabilities fall back to the nearest level.
	resp.Answers["tag.1"] = Answer{Type: TypeScore, Score: f(1.2), Probabilities: json.RawMessage(`{"Low":0.9}`)}
	reqs, _ = ToAssessments(&pb.Item{Id: 1}, 1, []BoundQuestion{q}, resp)
	if got, want := reqs[0].Note, "Low (1.2/4)"; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

func TestToAssessments_NoulAndTagIDs(t *testing.T) {
	qs := []BoundQuestion{noulQuestion(0), noulQuestion(9)}
	resp := &Response{Answers: map[string]Answer{
		"item":  {Type: TypeNoul, Noul: f(0.83)},
		"tag.9": {Type: TypeNoul, Noul: f(0)},
		"extra": {Type: TypeNoul, Noul: f(5)}, // not asked: ignored
	}}
	reqs, err := ToAssessments(&pb.Item{Id: 2}, 4, qs, resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 || reqs[0].TagId != 0 || *reqs[0].Score != 0.83 || reqs[0].Note != "" || reqs[1].TagId != 9 || *reqs[1].Score != 0 {
		t.Errorf("requests = %v", reqs)
	}
}

func TestCheckAnswers_Rejects(t *testing.T) {
	qs := []BoundQuestion{noulQuestion(0), scoreQuestion(5, 5)}
	good := func() map[string]Answer {
		return map[string]Answer{
			"item":  {Type: TypeNoul, Noul: f(0.5)},
			"tag.5": {Type: TypeScore, Score: f(2)},
		}
	}
	if err := CheckAnswers(qs, &Response{Answers: good()}); err != nil {
		t.Fatalf("good response rejected: %v", err)
	}
	// Values at the edges are fine.
	edge := good()
	edge["item"] = Answer{Type: TypeNoul, Noul: f(1)}
	edge["tag.5"] = Answer{Type: TypeScore, Score: f(4)}
	if err := CheckAnswers(qs, &Response{Answers: edge}); err != nil {
		t.Fatalf("edge response rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(m map[string]Answer)
		want   string
	}{
		{"missing id", func(m map[string]Answer) { delete(m, "tag.5") }, "no answer"},
		{"wrong type", func(m map[string]Answer) { m["item"] = Answer{Type: TypeScore, Score: f(0.5)} }, "answer type"},
		{"no type", func(m map[string]Answer) { m["item"] = Answer{Noul: f(0.5)} }, "answer type"},
		{"noul missing value", func(m map[string]Answer) { m["item"] = Answer{Type: TypeNoul} }, "no noul value"},
		{"noul NaN", func(m map[string]Answer) { m["item"] = Answer{Type: TypeNoul, Noul: f(math.NaN())} }, "not in 0 to 1"},
		{"noul Inf", func(m map[string]Answer) { m["item"] = Answer{Type: TypeNoul, Noul: f(math.Inf(1))} }, "not in 0 to 1"},
		{"noul above 1", func(m map[string]Answer) { m["item"] = Answer{Type: TypeNoul, Noul: f(1.2)} }, "not in 0 to 1"},
		{"noul below 0", func(m map[string]Answer) { m["item"] = Answer{Type: TypeNoul, Noul: f(-0.2)} }, "not in 0 to 1"},
		{"score missing value", func(m map[string]Answer) { m["tag.5"] = Answer{Type: TypeScore} }, "no score value"},
		{"score NaN", func(m map[string]Answer) { m["tag.5"] = Answer{Type: TypeScore, Score: f(math.NaN())} }, "not in 0 to"},
		{"score -Inf", func(m map[string]Answer) { m["tag.5"] = Answer{Type: TypeScore, Score: f(math.Inf(-1))} }, "not in 0 to"},
		{"score above top", func(m map[string]Answer) { m["tag.5"] = Answer{Type: TypeScore, Score: f(4.5)} }, "not in 0 to 4"},
		{"score below 0", func(m map[string]Answer) { m["tag.5"] = Answer{Type: TypeScore, Score: f(-1)} }, "not in 0 to 4"},
		{"confidence NaN", func(m map[string]Answer) {
			m["tag.5"] = Answer{Type: TypeScore, Score: f(1), Confidence: f(math.NaN())}
		}, "confidence"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := good()
			tc.mutate(m)
			err := CheckAnswers(qs, &Response{Answers: m})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
			if reqs, err := ToAssessments(&pb.Item{Id: 1}, 1, qs, &Response{Answers: m}); err == nil || reqs != nil {
				t.Errorf("ToAssessments must write nothing for a bad answer, got %v, %v", reqs, err)
			}
		})
	}
	if err := CheckAnswers(qs, nil); err == nil {
		t.Error("a nil response must be rejected")
	}
}
