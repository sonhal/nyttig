package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "nyttig.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func testSource(t *testing.T, database *sql.DB, name string) int64 {
	t.Helper()
	id, err := InsertSource(database, &Source{Name: name, URL: "http://example.com/" + name, Type: "rss", RefreshSec: 60, Enabled: true})
	if err != nil {
		t.Fatalf("InsertSource: %v", err)
	}
	return id
}

// storedPublished returns the text of items.published as SQLite holds it.
// Concatenating ” drops the DATETIME column type, so the driver returns the
// text instead of parsing it into a time.
func storedPublished(t *testing.T, database *sql.DB, guid string) string {
	t.Helper()
	var s string
	if err := database.QueryRow(`SELECT published || '' FROM items WHERE guid = ?`, guid).Scan(&s); err != nil {
		t.Fatalf("read published of %q: %v", guid, err)
	}
	return s
}

func titles(items []*Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestListItems_NewestFirstAcrossTimezones is the reported repro: 09:00 UTC
// must sort above 08:00 UTC even when the older one is written as
// 10:00:00+02:00. InsertItem receives the offsets as they are.
func TestListItems_NewestFirstAcrossTimezones(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")

	plusTwo := time.FixedZone("", 2*3600)
	published := map[string]time.Time{
		"older (08:00Z as 10:00+02:00)":  time.Date(2024, 6, 15, 10, 0, 0, 0, plusTwo),
		"newer (09:00Z)":                 time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		"newest (09:30Z as 11:30+02:00)": time.Date(2024, 6, 15, 11, 30, 0, 0, plusTwo),
	}
	i := 0
	for title, tm := range published {
		tm := tm
		if _, _, err := InsertItem(database, &Item{SourceID: src, GUID: string(rune('a' + i)), Link: "l", Title: title, Published: &tm}); err != nil {
			t.Fatalf("InsertItem: %v", err)
		}
		i++
	}

	want := []string{"newest (09:30Z as 11:30+02:00)", "newer (09:00Z)", "older (08:00Z as 10:00+02:00)"}
	items, _, err := ListItems(database, ItemFilter{})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if got := titles(items); !equalStrings(got, want) {
		t.Errorf("newest = %v, want %v", got, want)
	}

	// Paging through offsets walks the same order.
	var paged []string
	for off := 0; off < 3; off++ {
		page, _, err := ListItems(database, ItemFilter{Limit: 1, Offset: off})
		if err != nil {
			t.Fatalf("ListItems offset %d: %v", off, err)
		}
		paged = append(paged, titles(page)...)
	}
	if !equalStrings(paged, want) {
		t.Errorf("paged = %v, want %v", paged, want)
	}

	oldest, _, err := ListItems(database, ItemFilter{Sort: "oldest"})
	if err != nil {
		t.Fatalf("ListItems oldest: %v", err)
	}
	if got := titles(oldest); !equalStrings(got, []string{want[2], want[1], want[0]}) {
		t.Errorf("oldest = %v", got)
	}
}

// TestInsertItem_PublishedStoredAsUTCText pins the byte shape of what
// InsertItem stores. Migration 000003 rewrites old
// rows to this exact text, so the two must agree.
func TestInsertItem_PublishedStoredAsUTCText(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")

	// A fractional second in another offset is normalized by InsertItem.
	tm := time.Date(2024, 6, 15, 11, 5, 7, 900_000_000, time.FixedZone("", 2*3600))
	if _, _, err := InsertItem(database, &Item{SourceID: src, GUID: "g", Link: "l", Title: "t", Published: &tm}); err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	if got, want := storedPublished(t, database, "g"), "2024-06-15 09:05:07+00:00"; got != want {
		t.Fatalf("stored published = %q, want %q", got, want)
	}

	// Reading it back gives the same instant, in UTC.
	item, err := GetItem(database, 1)
	if err != nil || item == nil || item.Published == nil {
		t.Fatalf("GetItem = %v, %v", item, err)
	}
	if want := time.Date(2024, 6, 15, 9, 5, 7, 0, time.UTC); !item.Published.Equal(want) {
		t.Errorf("read back %v, want %v", item.Published, want)
	}
	if _, off := item.Published.Zone(); off != 0 {
		t.Errorf("read back offset = %d, want 0", off)
	}
}

// TestReadPublished_TrailingZ checks the read side of the driver: text with
// a trailing Z (what other tools and RFC 3339 write) is parsed as UTC.
func TestReadPublished_TrailingZ(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")

	for i, text := range []string{"2024-06-15T09:00:00Z", "2024-06-15 09:00:00+00:00", "2024-06-15T11:00:00+02:00"} {
		if _, err := database.Exec(`INSERT INTO items (source_id, guid, link, title, published) VALUES (?, ?, 'l', 't', ?)`,
			src, string(rune('a'+i)), text); err != nil {
			t.Fatalf("insert %q: %v", text, err)
		}
	}
	items, _, err := ListItems(database, ItemFilter{})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items", len(items))
	}
	want := time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC)
	for _, it := range items {
		if it.Published == nil || !it.Published.Equal(want) {
			t.Errorf("item %q published = %v, want %v", it.GUID, it.Published, want)
		}
	}
}

// TestMigration_NormalizesPublished builds a database at version 2, writes
// rows the way the old fetcher did, applies the remaining migrations and
// checks that the old rows end up byte-for-byte like new inserts and sort
// correctly next to them.
func TestMigration_NormalizesPublished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}

	// Apply only migrations 1 and 2 by hand.
	for _, name := range []string{"000001_init.up.sql", "000002_source_color_abbreviation.up.sql"} {
		b, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := database.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, dirty BOOLEAN NOT NULL DEFAULT 0);
		INSERT INTO schema_migrations (version) VALUES (1), (2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO sources (name, url) VALUES ('s', 'http://x')`); err != nil {
		t.Fatal(err)
	}

	// What the old code wrote: the parsed time in its own offset, with fractions.
	plusTwo := time.FixedZone("", 2*3600)
	old := map[string]*time.Time{
		"plus2":    ptr(time.Date(2024, 6, 15, 10, 0, 0, 0, plusTwo)),
		"zulu":     ptr(time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC)),
		"fraction": ptr(time.Date(2024, 6, 15, 9, 0, 0, 500_000_000, time.UTC)),
		"minus5":   ptr(time.Date(2024, 6, 15, 4, 0, 0, 0, time.FixedZone("", -5*3600))),
		"null":     nil,
	}
	for guid, tm := range old {
		var v any
		if tm != nil {
			v = *tm
		}
		if _, err := database.Exec(`INSERT INTO items (source_id, guid, link, title, published) VALUES (1, ?, 'l', ?, ?)`, guid, guid, v); err != nil {
			t.Fatal(err)
		}
	}
	// An unparseable value is left alone rather than erased.
	if _, err := database.Exec(`INSERT INTO items (source_id, guid, link, title, published) VALUES (1, 'junk', 'l', 'junk', 'not a date')`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	// Open runs the pending migrations.
	migrated, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = migrated.Close() }()

	// Rows written before and after the migration share one shape.
	fresh := time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC)
	if _, _, err := InsertItem(migrated, &Item{SourceID: 1, GUID: "fresh", Link: "l", Title: "fresh", Published: &fresh}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"plus2":    "2024-06-15 08:00:00+00:00",
		"zulu":     "2024-06-15 09:00:00+00:00",
		"fraction": "2024-06-15 09:00:00+00:00",
		"minus5":   "2024-06-15 09:00:00+00:00",
		"fresh":    "2024-06-15 09:00:00+00:00",
		"junk":     "not a date",
	}
	for guid, w := range want {
		if got := storedPublished(t, migrated, guid); got != w {
			t.Errorf("%s: stored %q, want %q", guid, got, w)
		}
	}
	var nulls int
	if err := migrated.QueryRow(`SELECT count(*) FROM items WHERE published IS NULL`).Scan(&nulls); err != nil || nulls != 1 {
		t.Errorf("NULL published rows = %d (%v), want 1", nulls, err)
	}

	// The 08:00Z row (written as 10:00+02:00) sorts below the 09:00Z rows.
	items, _, err := ListItems(migrated, ItemFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var plus2At int
	for i, it := range items {
		if it.GUID == "plus2" {
			plus2At = i
		}
	}
	// 4 rows at 09:00Z, then plus2, then junk/null (junk sorts as text, so only
	// check plus2 is below the four 09:00Z rows).
	if plus2At < 4 {
		t.Errorf("plus2 sorted at %d, want below the four 09:00Z rows: %v", plus2At, titles(items))
	}

	// Migrating again changes nothing.
	if err := runMigrations(migrated); err != nil {
		t.Fatalf("second runMigrations: %v", err)
	}
}

func ptr(t time.Time) *time.Time { return &t }

// queryPlan returns the EXPLAIN QUERY PLAN detail lines of a query.
func queryPlan(t *testing.T, database *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	return plan
}

// TestIndexes_UsedByHotQueries checks with EXPLAIN QUERY PLAN that the
// newest-first listing walks idx_items_published instead of sorting the
// table, and that tag filters use idx_item_tags_tag_id.
func TestIndexes_UsedByHotQueries(t *testing.T) {
	database := openTestDB(t)

	// The exact ORDER BY of ListItems for the default sort.
	plan := strings.Join(queryPlan(t, database, `SELECT i.id FROM items i JOIN sources s ON s.id = i.source_id
		ORDER BY i.published DESC NULLS LAST, i.fetched_at DESC LIMIT 100 OFFSET 0`), "\n")
	if !strings.Contains(plan, "idx_items_published") {
		t.Errorf("newest-first plan does not use idx_items_published:\n%s", plan)
	}
	if strings.Contains(plan, "TEMP B-TREE") {
		t.Errorf("newest-first plan sorts in a temp b-tree:\n%s", plan)
	}

	plan = strings.Join(queryPlan(t, database, `SELECT COUNT(*) FROM item_tags WHERE tag_id = ?`, 1), "\n")
	if !strings.Contains(plan, "idx_item_tags_tag_id") {
		t.Errorf("tag lookup plan does not use idx_item_tags_tag_id:\n%s", plan)
	}
	plan = strings.Join(queryPlan(t, database, `SELECT i.id FROM items i WHERE i.id IN (SELECT item_id FROM item_tags WHERE tag_id = ?)`, 1), "\n")
	if !strings.Contains(plan, "idx_item_tags_tag_id") {
		t.Errorf("tag filter plan does not use idx_item_tags_tag_id:\n%s", plan)
	}
}
