package main

import (
	"testing"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestFormatViewFilter(t *testing.T) {
	sources := map[int64]string{1: "HN", 2: "Big Feed"}
	tags := map[int64]string{5: "cyber security", 6: "rust", 7: "#odd"}
	assessors := map[int64]string{1: "claude", 2: "my bot"}
	min := 0.7
	zero := 0.0
	for _, tc := range []struct {
		name string
		f    *pb.ViewFilter
		want string
	}{
		{"nil", nil, ""},
		{"empty", &pb.ViewFilter{}, ""},
		{"default sort is left out", &pb.ViewFilter{Sort: "newest"}, ""},
		{"all fields", &pb.ViewFilter{Search: "go  sqlite", SourceId: 1, TagId: 5, UnviewedOnly: true, Sort: "oldest"},
			`go sqlite src:HN tag:"cyber security" is:unviewed sort:oldest`},
		{"quoted source name", &pb.ViewFilter{SourceId: 2}, `src:"Big Feed"`},
		{"name starting with hash", &pb.ViewFilter{TagId: 7}, `tag:"#odd"`},
		{"unknown ids", &pb.ViewFilter{SourceId: 9, TagId: 8}, `src:#9 tag:#8`},
		{"operator-looking word is quoted", &pb.ViewFilter{Search: "tag:x plain"}, `"tag:x" plain`},
		{"assessor alone", &pb.ViewFilter{AssessorId: 1}, `score:claude`},
		{"assessor with minimum", &pb.ViewFilter{AssessorId: 1, MinScore: &min}, `score:claude>=0.7`},
		{"minimum zero is kept", &pb.ViewFilter{AssessorId: 1, MinScore: &zero}, `score:claude>=0`},
		{"quoted assessor and score sort", &pb.ViewFilter{AssessorId: 2, MinScore: &min, Sort: "score"}, `score:"my bot">=0.7 sort:score`},
		{"unassessed", &pb.ViewFilter{UnassessedBy: 1, TagId: 6}, `tag:rust unassessed:claude`},
		{"unknown assessor", &pb.ViewFilter{AssessorId: 9}, `score:#9`},
		{"score words are quoted", &pb.ViewFilter{Search: "score:x unassessed:y"}, `"score:x" "unassessed:y"`},
		{"other colon words stay", &pb.ViewFilter{Search: "http://x"}, `http://x`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatViewFilter(tc.f, sources, tags, assessors); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFindView(t *testing.T) {
	views := []*pb.SavedView{{Id: 1, Name: "Security"}, {Id: 2, Name: "2024"}, {Id: 3, Name: "x"}}
	for _, tc := range []struct {
		ref    string
		wantID int64
		ok     bool
	}{
		{"1", 1, true},
		{"security", 1, true},
		{"2024", 2, true}, // numeric but no such ID: falls back to the name
		{"3", 3, true},
		{"nope", 0, false},
		{"9", 0, false},
	} {
		v, err := findView(views, tc.ref)
		if (err == nil) != tc.ok || (tc.ok && v.Id != tc.wantID) {
			t.Errorf("findView(%q) = %v, %v", tc.ref, v, err)
		}
	}
}

func TestFindAssessor(t *testing.T) {
	list := []*pb.Assessor{{Id: 1, Name: "claude"}, {Id: 2, Name: "7"}, {Id: 7, Name: "cvss"}}
	for _, tc := range []struct {
		ref    string
		wantID int64
		ok     bool
	}{
		{"claude", 1, true},
		{"1", 1, true},
		{"7", 7, true}, // an ID wins over a name that looks like a number
		{"cvss", 7, true},
		{"Claude", 0, false}, // names are exact
		{"nope", 0, false},
		{"9", 0, false},
	} {
		a, err := findAssessor(list, tc.ref)
		if (err == nil) != tc.ok || (tc.ok && a.Id != tc.wantID) {
			t.Errorf("findAssessor(%q) = %v, %v", tc.ref, a, err)
		}
	}
}

func TestFormatItemScores(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	it := &pb.Item{Assessments: []*pb.Assessment{
		{AssessorId: 1, AssessorName: "claude", Score: f(0.3)},
		{AssessorId: 1, AssessorName: "claude", TagId: 4, Score: f(0.9)},
		{AssessorId: 2, AssessorName: "cvss", Score: f(0.98)},
		{AssessorId: 3, AssessorName: "note-only", Note: "n"},
		{AssessorId: 4, AssessorName: "zero", Score: f(0)},
	}}
	if got, want := formatItemScores(it, 0), "claude 0.90, cvss 0.98, zero 0.00"; got != want {
		t.Errorf("no selection: got %q, want %q", got, want)
	}
	if got, want := formatItemScores(it, 2), "cvss 0.98, claude 0.90, zero 0.00"; got != want {
		t.Errorf("cvss selected: got %q, want %q", got, want)
	}
	if got := formatItemScores(&pb.Item{}, 1); got != "" {
		t.Errorf("no assessments: got %q", got)
	}
	if got := formatItemScores(&pb.Item{Assessments: []*pb.Assessment{{AssessorId: 3, AssessorName: "n", Note: "x"}}}, 3); got != "" {
		t.Errorf("only a note: got %q", got)
	}
	if got := formatScore(0.35); got != "0.35" {
		t.Errorf("formatScore(0.35) = %q", got)
	}
	if got := formatScore(1); got != "1" {
		t.Errorf("formatScore(1) = %q", got)
	}
}

func TestSplitItemArg(t *testing.T) {
	item, rest := splitItemArg([]string{"12", "-assessor", "claude"})
	if item != "12" || len(rest) != 2 {
		t.Errorf("got %q, %v", item, rest)
	}
	item, rest = splitItemArg([]string{"-assessor", "claude", "-item", "12"})
	if item != "" || len(rest) != 4 {
		t.Errorf("flags first: got %q, %v", item, rest)
	}
	if _, err := parseItemID("x"); err == nil {
		t.Error("parseItemID accepted x")
	}
	if _, err := parseItemID("0"); err == nil {
		t.Error("parseItemID accepted 0")
	}
}
