package tagger

import (
	"reflect"
	"testing"
)

func mustCompile(t *testing.T, rules ...TagRule) []CompiledRule {
	t.Helper()
	compiled, bad := Compile(rules)
	if len(bad) != 0 {
		t.Fatalf("Compile: unexpected bad rules %+v", bad)
	}
	return compiled
}

func TestCompiledRule_Matches(t *testing.T) {
	item := Item{ID: 1, SourceID: 5, Title: "Rust 1.85 released", Description: "Go and Zig news"}
	tests := []struct {
		name string
		rule TagRule
		want bool
	}{
		{"title field matches title", TagRule{Field: "title", Pattern: `Rust`}, true},
		{"title field ignores description", TagRule{Field: "title", Pattern: `Zig`}, false},
		{"description field matches description", TagRule{Field: "description", Pattern: `Zig`}, true},
		{"description field ignores title", TagRule{Field: "description", Pattern: `Rust`}, false},
		{"both matches title", TagRule{Field: "both", Pattern: `Rust`}, true},
		{"both matches description", TagRule{Field: "both", Pattern: `Zig`}, true},
		{"empty field is both", TagRule{Field: "", Pattern: `Zig`}, true},
		{"unknown field is both", TagRule{Field: "body", Pattern: `Zig`}, true},
		{"no match", TagRule{Field: "both", Pattern: `Haskell`}, false},
		{"global rule", TagRule{SourceID: 0, Field: "title", Pattern: `Rust`}, true},
		{"rule for the item's source", TagRule{SourceID: 5, Field: "title", Pattern: `Rust`}, true},
		{"rule for another source", TagRule{SourceID: 6, Field: "title", Pattern: `Rust`}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := mustCompile(t, tt.rule)[0]
			if got := cr.Matches(item); got != tt.want {
				t.Errorf("Matches = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompile_ReturnsBadRules(t *testing.T) {
	compiled, bad := Compile([]TagRule{
		{ID: 1, TagID: 10, Pattern: `ok`},
		{ID: 2, TagID: 11, Pattern: `[unclosed`},
		{ID: 3, TagID: 12, Pattern: `fine`},
	})
	if len(compiled) != 2 || compiled[0].Rule.ID != 1 || compiled[1].Rule.ID != 3 {
		t.Errorf("compiled = %+v, want rules 1 and 3 in order", compiled)
	}
	if len(bad) != 1 || bad[0].Rule.ID != 2 || bad[0].Err == nil {
		t.Errorf("bad = %+v, want rule 2 with an error", bad)
	}
}

func TestDesired(t *testing.T) {
	rules := mustCompile(t,
		TagRule{ID: 1, TagID: 20, Field: "title", Pattern: `(?i)rust`},
		TagRule{ID: 2, TagID: 20, Field: "description", Pattern: `(?i)cargo`}, // second rule, same tag
		TagRule{ID: 3, TagID: 10, Field: "both", Pattern: `(?i)release`},
		TagRule{ID: 4, TagID: 30, SourceID: 2, Field: "both", Pattern: `.`}, // only source 2
	)
	items := []Item{
		{ID: 100, SourceID: 1, Title: "Rust release", Description: "cargo too"}, // both rules of tag 20, and tag 10
		{ID: 101, SourceID: 1, Title: "Nothing here"},
		{ID: 102, SourceID: 2, Title: "Anything"},
		{ID: 103, SourceID: 1, Title: "News", Description: "Cargo update"},
	}
	got := Desired(rules, items)
	want := map[int64][]int64{
		100: {10, 20},
		102: {30},
		103: {20},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Desired = %v, want %v", got, want)
	}
}
