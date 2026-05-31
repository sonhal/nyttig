package tagger

import (
	"fmt"
	"sync"
	"testing"
)

// mockStore implements RuleStore for testing, capturing AssignTag calls.
type mockStore struct {
	rules []TagRule
	// assigned records (itemID, tagID) pairs in insertion order.
	assigned []tagAssignment
	// failAssignAt causes AssignTag to return an error after n successful calls.
	failAssignAt int
}

type tagAssignment struct {
	itemID int64
	tagID  int64
}

func (m *mockStore) LoadRules() ([]TagRule, error) {
	return m.rules, nil
}

func (m *mockStore) AssignTag(itemID, tagID int64) error {
	if m.failAssignAt > 0 && len(m.assigned) >= m.failAssignAt {
		return fmt.Errorf("simulated error")
	}
	m.assigned = append(m.assigned, tagAssignment{itemID, tagID})
	return nil
}

func TestTagItem_GlobalRule_BothFields(t *testing.T) {
	store := &mockStore{
		rules: []TagRule{
			{ID: 1, SourceID: 0, TagID: 10, TagName: "rust", Field: "both", Pattern: `(?i)\brust\b`},
		},
	}
	tagger := New(store, nil)

	// Title match
	n, err := tagger.TagItem(Item{ID: 100, SourceID: 5, Title: "Announcing Rust 1.85", Description: "A new release"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 tag assigned, got %d", n)
	}
	if len(store.assigned) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(store.assigned))
	}
	if store.assigned[0].itemID != 100 || store.assigned[0].tagID != 10 {
		t.Errorf("wrong assignment: %+v", store.assigned[0])
	}

	// Description match
	store.assigned = nil
	n, err = tagger.TagItem(Item{ID: 101, SourceID: 5, Title: "Weekly update", Description: "rust is great"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 tag assigned for description match, got %d", n)
	}

	// No match
	store.assigned = nil
	n, err = tagger.TagItem(Item{ID: 102, SourceID: 5, Title: "Weekly update", Description: "Python is great"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 tags, got %d", n)
	}
	if len(store.assigned) != 0 {
		t.Fatalf("expected 0 assignments, got %d", len(store.assigned))
	}
}

func TestTagItem_PerSourceRule(t *testing.T) {
	store := &mockStore{
		rules: []TagRule{
			{ID: 1, SourceID: 3, TagID: 20, TagName: "go", Field: "both", Pattern: `(?i)\bgolang?\b`},
		},
	}
	tagger := New(store, nil)

	// Item from matching source
	n, err := tagger.TagItem(Item{ID: 200, SourceID: 3, Title: "Golang 1.24 Released", Description: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 tag for matching source, got %d", n)
	}

	// Item from different source
	store.assigned = nil
	n, err = tagger.TagItem(Item{ID: 201, SourceID: 7, Title: "Golang 1.24 Released", Description: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 tags for non-matching source, got %d", n)
	}
}

func TestTagItem_GlobalAndPerSource(t *testing.T) {
	store := &mockStore{
		rules: []TagRule{
			{ID: 1, SourceID: 0, TagID: 10, TagName: "db", Field: "both", Pattern: `(?i)(sqlite|postgres|mysql|database)`},
			{ID: 2, SourceID: 5, TagID: 20, TagName: "hn", Field: "title", Pattern: `(?i)Show HN`},
			{ID: 3, SourceID: 0, TagID: 30, TagName: "go", Field: "both", Pattern: `(?i)\bgolang?\b`},
		},
	}
	tagger := New(store, nil)

	// Item from source 5 matching only global rule "db"
	n, err := tagger.TagItem(Item{ID: 300, SourceID: 5, Title: "New SQLite Release", Description: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 tag (db), got %d", n)
	}

	// Item from source 5 matching both global + per-source + another global
	store.assigned = nil
	n, err = tagger.TagItem(Item{ID: 301, SourceID: 5, Title: "Show HN: A Golang SQLite Library", Description: "database"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 tags (db, hn, go), got %d", n)
	}

	// Item from source 7 (no per-source rule)
	store.assigned = nil
	n, err = tagger.TagItem(Item{ID: 302, SourceID: 7, Title: "Show HN: A Golang Tool", Description: "uses postgres"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		// db (global) + go (global), no hn because hn is per-source 5
		t.Fatalf("expected 2 global tags (db + go), got %d", n)
	}
}

func TestTagItem_PriorityOrdering(t *testing.T) {
	store := &mockStore{
		rules: []TagRule{
			{ID: 1, SourceID: 0, TagID: 10, TagName: "low", Field: "both", Pattern: `(?i)test`, Priority: 100},
			{ID: 2, SourceID: 0, TagID: 20, TagName: "high", Field: "both", Pattern: `(?i)test`, Priority: 1},
			{ID: 3, SourceID: 0, TagID: 30, TagName: "mid", Field: "both", Pattern: `(?i)test`, Priority: 50},
		},
	}
	tagger := New(store, nil)

	n, err := tagger.TagItem(Item{ID: 400, SourceID: 1, Title: "test", Description: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 tags, got %d", n)
	}

	// Verify priority order: high (20, prio 1), mid (30, prio 50), low (10, prio 100)
	if len(store.assigned) != 3 {
		t.Fatalf("expected 3 assignments, got %d", len(store.assigned))
	}
	if store.assigned[0].tagID != 20 {
		t.Errorf("expected high-priority tag first, got tagID %d", store.assigned[0].tagID)
	}
	if store.assigned[1].tagID != 30 {
		t.Errorf("expected mid-priority tag second, got tagID %d", store.assigned[1].tagID)
	}
	if store.assigned[2].tagID != 10 {
		t.Errorf("expected low-priority tag last, got tagID %d", store.assigned[2].tagID)
	}
}

func TestTagItem_FieldScope(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		pattern  string
		title    string
		desc     string
		expected int
	}{
		{
			name:     "title-only matches title",
			field:    "title",
			pattern:  `(?i)rust`,
			title:    "Rust is great",
			desc:     "No match here",
			expected: 1,
		},
		{
			name:     "title-only does not match description",
			field:    "title",
			pattern:  `(?i)rust`,
			title:    "Weekly news",
			desc:     "Rust is great",
			expected: 0,
		},
		{
			name:     "description-only matches description",
			field:    "description",
			pattern:  `(?i)rust`,
			title:    "Weekly news",
			desc:     "Rust is great",
			expected: 1,
		},
		{
			name:     "description-only does not match title",
			field:    "description",
			pattern:  `(?i)rust`,
			title:    "Rust is great",
			desc:     "No match here",
			expected: 0,
		},
		{
			name:     "both matches title",
			field:    "both",
			pattern:  `(?i)rust`,
			title:    "Rust is great",
			desc:     "No match here",
			expected: 1,
		},
		{
			name:     "both matches description",
			field:    "both",
			pattern:  `(?i)rust`,
			title:    "Weekly news",
			desc:     "Rust is great",
			expected: 1,
		},
		{
			name:     "empty field defaults to both",
			field:    "",
			pattern:  `(?i)python`,
			title:    "Weekly news",
			desc:     "Python announcement",
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &mockStore{
				rules: []TagRule{
					{ID: 1, SourceID: 0, TagID: 10, Field: tt.field, Pattern: tt.pattern},
				},
			}
			tagger := New(store, nil)
			n, err := tagger.TagItem(Item{ID: 500, SourceID: 1, Title: tt.title, Description: tt.desc})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != tt.expected {
				t.Errorf("expected %d tags, got %d", tt.expected, n)
			}
		})
	}
}

func TestTagItem_MultiTagMatching(t *testing.T) {
	store := &mockStore{
		rules: []TagRule{
			{ID: 1, SourceID: 0, TagID: 10, TagName: "lang", Field: "both", Pattern: `(?i)(rust|go|python|java)\b`},
			{ID: 2, SourceID: 0, TagID: 20, TagName: "db", Field: "both", Pattern: `(?i)(sqlite|postgres|mysql|database)`},
			{ID: 3, SourceID: 0, TagID: 30, TagName: "linux", Field: "both", Pattern: `(?i)\blinux\b`},
			{ID: 4, SourceID: 0, TagID: 40, TagName: "web", Field: "both", Pattern: `(?i)(web|http|api|rest)`},
		},
	}
	tagger := New(store, nil)

	// Item matching multiple tags
	n, err := tagger.TagItem(Item{
		ID:          600,
		SourceID:    1,
		Title:       "Building a REST API with Rust and SQLite",
		Description: "Learn how to build a web service on Linux",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 tags (rust+db+linux+web), got %d", n)
	}

	// Verify all four tag IDs were assigned
	foundTags := make(map[int64]bool)
	for _, a := range store.assigned {
		foundTags[a.tagID] = true
	}
	for _, expected := range []int64{10, 20, 30, 40} {
		if !foundTags[expected] {
			t.Errorf("missing assignment for tagID %d", expected)
		}
	}
}

func TestTagItem_RegexPatterns(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		title    string
		desc     string
		expected int
	}{
		{
			name:     "simple literal",
			pattern:  `(?i)rust`,
			title:    "Rust announcement",
			expected: 1,
		},
		{
			name:     "word boundary",
			pattern:  `(?i)\bgo\b`,
			title:    "Let's go to the park",
			expected: 1,
		},
		{
			name:     "word boundary no false match",
			pattern:  `(?i)\bgo\b`,
			title:    "golang is cool",
			expected: 0,
		},
		{
			name:     "alternation group",
			pattern:  `(?i)(rust|cargo|crates)`,
			title:    "New cargo features",
			expected: 1,
		},
		{
			name:     "case insensitive",
			pattern:  `(?i)LINUX`,
			title:    "linux kernel update",
			expected: 1,
		},
		{
			name:     "multiline description match",
			pattern:  `(?i)breaking.change`,
			desc:     "This release includes a breaking change in the API",
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &mockStore{
				rules: []TagRule{
					{ID: 1, SourceID: 0, TagID: 10, Field: "both", Pattern: tt.pattern},
				},
			}
			tagger := New(store, nil)
			n, err := tagger.TagItem(Item{ID: 700, SourceID: 1, Title: tt.title, Description: tt.desc})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != tt.expected {
				t.Errorf("expected %d tags, got %d", tt.expected, n)
			}
		})
	}
}

func TestTagItem_InvalidRegexSkipsRule(t *testing.T) {
	store := &mockStore{
		rules: []TagRule{
			{ID: 1, SourceID: 0, TagID: 10, Field: "both", Pattern: `[invalid(`}, // invalid regex
			{ID: 2, SourceID: 0, TagID: 20, Field: "both", Pattern: `(?i)valid`},
		},
	}
	tagger := New(store, nil)

	n, err := tagger.TagItem(Item{ID: 800, SourceID: 1, Title: "valid test", Description: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 tag from valid rule, got %d", n)
	}
	if len(store.assigned) != 1 || store.assigned[0].tagID != 20 {
		t.Errorf("expected only tagID 20 assigned, got %+v", store.assigned)
	}
}

func TestTagItem_NoRules(t *testing.T) {
	store := &mockStore{rules: nil}
	tagger := New(store, nil)

	n, err := tagger.TagItem(Item{ID: 900, SourceID: 1, Title: "hello", Description: "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 tags with no rules, got %d", n)
	}
}

func TestTagItems_BatchProcessing(t *testing.T) {
	store := &mockStore{
		rules: []TagRule{
			{ID: 1, SourceID: 0, TagID: 10, TagName: "rust", Field: "both", Pattern: `(?i)\brust\b`},
			{ID: 2, SourceID: 3, TagID: 20, TagName: "hn", Field: "title", Pattern: `(?i)Show HN`},
		},
	}
	tagger := New(store, nil)

	items := []Item{
		{ID: 1, SourceID: 1, Title: "Rust 1.85 Released", Description: ""},
		{ID: 2, SourceID: 3, Title: "Show HN: Cool Tool", Description: ""},
		{ID: 3, SourceID: 4, Title: "Nothing here", Description: ""},
		{ID: 4, SourceID: 1, Title: "Rust is fast", Description: "also cool"},
	}

	n, err := tagger.TagItems(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Item 1: rust (global) -> 1 tag
	// Item 2: Show HN (per-source 3) -> 1 tag
	// Item 3: no match -> 0
	// Item 4: rust (global) -> 1 tag
	// Total: 3
	if n != 3 {
		t.Fatalf("expected 3 total tags, got %d", n)
	}

	if len(store.assigned) != 3 {
		t.Fatalf("expected 3 assignments, got %d", len(store.assigned))
	}
}

// safeStore is a concurrency-safe mock used for the concurrent test.
type safeStore struct {
	rules    []TagRule
	mu       sync.Mutex
	assigned []tagAssignment
}

func (s *safeStore) LoadRules() ([]TagRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TagRule{}, s.rules...), nil
}

func (s *safeStore) AssignTag(itemID, tagID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assigned = append(s.assigned, tagAssignment{itemID, tagID})
	return nil
}

func TestTagItem_ConcurrentSafety(t *testing.T) {
	ss := &safeStore{
		rules: []TagRule{
			{ID: 1, SourceID: 0, TagID: 10, TagName: "test", Field: "both", Pattern: `(?i)test`},
		},
	}
	tagger := New(ss, nil)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = tagger.TagItem(Item{ID: 999, SourceID: 1, Title: "test", Description: ""})
		}()
	}
	wg.Wait()
	// No panic = pass
}

func TestTagItem_UnknownFieldDefaults(t *testing.T) {
	store := &mockStore{
		rules: []TagRule{
			{ID: 1, SourceID: 0, TagID: 10, Field: "bogus_field", Pattern: `(?i)hello`},
		},
	}
	tagger := New(store, nil)

	// Matches title
	n, err := tagger.TagItem(Item{ID: 1, SourceID: 1, Title: "hello world", Description: "no match"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("unknown field should default to both matching title, got %d", n)
	}

	// Matches description
	store.assigned = nil
	n, err = tagger.TagItem(Item{ID: 2, SourceID: 1, Title: "no match", Description: "hello world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("unknown field should default to both matching description, got %d", n)
	}
}
