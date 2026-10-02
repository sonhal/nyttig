package db

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// addDated inserts an item and, when published is nil, backdates fetched_at
// to the given text (the column's own shape, no suffix).
func addDated(t *testing.T, database *sql.DB, src int64, guid, title string, published *time.Time, fetchedAt string) int64 {
	t.Helper()
	id, _, err := InsertItem(database, &Item{SourceID: src, GUID: guid, Link: "l", Title: title, Published: published})
	if err != nil {
		t.Fatalf("InsertItem %s: %v", guid, err)
	}
	if fetchedAt != "" {
		if _, err := database.Exec(`UPDATE items SET fetched_at = ? WHERE id = ?`, fetchedAt, id); err != nil {
			t.Fatalf("backdate %s: %v", guid, err)
		}
	}
	return id
}

func listAfter(t *testing.T, database *sql.DB, f ItemFilter) ([]string, int) {
	t.Helper()
	f.Sort = "oldest"
	items, total, err := ListItems(database, f)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	return titles(items), total
}

func TestListItems_After(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	cutoff := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { p := cutoff.Add(d); return &p }

	addDated(t, database, src, "pub-old", "pub old", at(-time.Second), "")
	addDated(t, database, src, "pub-at", "pub at cutoff", at(0), "")
	addDated(t, database, src, "pub-new", "pub new", at(time.Hour), "")
	addDated(t, database, src, "pub-future", "pub future", at(1000*time.Hour), "")
	// No published date: the fetch time decides, in fetched_at's own shape.
	addDated(t, database, src, "nodate-old", "nodate old", nil, "2026-09-01 11:59:59")
	addDated(t, database, src, "nodate-at", "nodate at cutoff", nil, "2026-09-01 12:00:00")
	addDated(t, database, src, "nodate-new", "nodate new", nil, "2026-09-02 00:00:00")
	// A published date wins over an old fetch time.
	addDated(t, database, src, "pub-vs-fetch", "pub beats fetch", at(2*time.Hour), "2020-01-01 00:00:00")

	got, total := listAfter(t, database, ItemFilter{After: cutoff})
	want := []string{"nodate at cutoff", "pub at cutoff", "pub new", "pub beats fetch", "pub future", "nodate new"}
	if total != len(want) || len(got) != len(want) {
		t.Fatalf("total %d, got %v", total, got)
	}
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("missing %q in %v", w, got)
		}
	}

	// A cutoff given in another zone is the same instant.
	zoned := cutoff.In(time.FixedZone("", 2*3600))
	if _, total2 := listAfter(t, database, ItemFilter{After: zoned}); total2 != total {
		t.Errorf("zoned cutoff total = %d, want %d", total2, total)
	}
	// Zero = no window.
	if _, all := listAfter(t, database, ItemFilter{}); all != 8 {
		t.Errorf("no window total = %d, want 8", all)
	}
}

func TestListItems_AfterCombinesWithOtherFilters(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	other := testSource(t, database, "other")
	tag := mustTag(t, database, "rust")
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour)
	recent := cutoff.Add(24 * time.Hour)

	a := addDated(t, database, src, "a", "rust async new", &recent, "")
	b := addDated(t, database, src, "b", "rust async old", &old, "")
	c := addDated(t, database, other, "c", "rust async other", &recent, "")
	d := addDated(t, database, src, "d", "plain new", &recent, "")
	for _, id := range []int64{a, b, c} {
		if _, err := database.Exec(`INSERT INTO item_tags (item_id, tag_id) VALUES (?, ?)`, id, tag); err != nil {
			t.Fatal(err)
		}
	}
	if err := MarkViewed(database, []int64{a}); err != nil {
		t.Fatal(err)
	}
	_ = d

	cases := []struct {
		name string
		f    ItemFilter
		want []string
	}{
		{"window only", ItemFilter{After: cutoff}, []string{"plain new", "rust async new", "rust async other"}},
		{"tag", ItemFilter{After: cutoff, TagID: tag}, []string{"rust async new", "rust async other"}},
		{"tag+source", ItemFilter{After: cutoff, TagID: tag, SourceID: src}, []string{"rust async new"}},
		{"search", ItemFilter{After: cutoff, Search: "async"}, []string{"rust async new", "rust async other"}},
		{"unviewed", ItemFilter{After: cutoff, UnviewedOnly: true, TagID: tag}, []string{"rust async other"}},
	}
	for _, tc := range cases {
		got, total := listAfter(t, database, tc.f)
		if total != len(tc.want) {
			t.Errorf("%s: total = %d, want %d (%v)", tc.name, total, len(tc.want), got)
		}
		g := map[string]bool{}
		for _, x := range got {
			g[x] = true
		}
		for _, w := range tc.want {
			if !g[w] {
				t.Errorf("%s: missing %q in %v", tc.name, w, got)
			}
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestListItems_AfterCountMatchesPages(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		p := base.Add(time.Duration(i) * time.Hour)
		addDated(t, database, src, string(rune('a'+i)), string(rune('a'+i)), &p, "")
	}
	after := base.Add(3 * time.Hour)
	var all []string
	for off := 0; off < 10; off += 3 {
		items, total, err := ListItems(database, ItemFilter{After: after, Limit: 3, Offset: off, Sort: "oldest"})
		if err != nil {
			t.Fatal(err)
		}
		if total != 7 {
			t.Fatalf("total = %d, want 7", total)
		}
		all = append(all, titles(items)...)
	}
	if want := []string{"d", "e", "f", "g", "h", "i", "j"}; !reflect.DeepEqual(all, want) {
		t.Errorf("pages = %v, want %v", all, want)
	}
}

func TestSavedView_SinceRoundTrip(t *testing.T) {
	database := openTestDB(t)
	id := addView(t, database, &SavedView{Name: "week", Since: "7d"})
	got, _ := GetSavedView(database, id)
	if got.Since != "7d" {
		t.Fatalf("Since = %q", got.Since)
	}
	got.Since = "1mo"
	if ok, err := UpdateSavedView(database, got); err != nil || !ok {
		t.Fatalf("update = %v, %v", ok, err)
	}
	list, _ := ListSavedViews(database)
	if len(list) != 1 || list[0].Since != "1mo" {
		t.Fatalf("list = %+v", list[0])
	}
	got.Since = ""
	_, _ = UpdateSavedView(database, got)
	again, _ := GetSavedView(database, id)
	if again.Since != "" {
		t.Fatalf("cleared Since = %q", again.Since)
	}
}
