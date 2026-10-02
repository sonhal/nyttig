package main

import (
	"testing"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestFormatViewFilter(t *testing.T) {
	sources := map[int64]string{1: "HN", 2: "Big Feed"}
	tags := map[int64]string{5: "cyber security", 6: "rust", 7: "#odd"}
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
		{"other colon words stay", &pb.ViewFilter{Search: "http://x"}, `http://x`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatViewFilter(tc.f, sources, tags); got != tc.want {
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
