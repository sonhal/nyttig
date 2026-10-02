package db

import (
	"path/filepath"
	"regexp"
	"testing"
)

// TestOpen_MigratesAndSearches opens a real on-disk database, which runs the
// embedded migrations (including the FTS5 virtual table), and checks that
// full-text search works end to end. This guards against building the SQLite
// driver without FTS5, which makes the daemon fail at startup.
func TestOpen_MigratesAndSearches(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "nyttig.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	srcID, err := InsertSource(database, &Source{Name: "Test", URL: "http://example.com/feed", Type: "rss", RefreshSec: 60, Enabled: true})
	if err != nil {
		t.Fatalf("InsertSource: %v", err)
	}

	for i, title := range []string{"SQLite 3.47 released", "Rust 1.90 announced"} {
		item := &Item{SourceID: srcID, GUID: string(rune('a' + i)), Link: "http://example.com/" + title, Title: title}
		if _, _, err := InsertItem(database, item); err != nil {
			t.Fatalf("InsertItem(%q): %v", title, err)
		}
	}

	items, total, err := SearchItems(database, "sqlite", 0, 0, 10, 0)
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Title != "SQLite 3.47 released" {
		t.Fatalf("SearchItems(sqlite) = %d items (total %d), want the SQLite item", len(items), total)
	}
}

// TestOpen_ReportsFTS5AndVersion checks the startup probes Open relies on:
// the compile-option query finds FTS5 in the library this test binary was
// built against, and the version query returns a SQLite 3.x version.
func TestOpen_ReportsFTS5AndVersion(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "nyttig.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	if err := requireFTS5(database); err != nil {
		t.Fatalf("requireFTS5: %v", err)
	}
	v, err := SQLiteVersion(database)
	if err != nil {
		t.Fatalf("SQLiteVersion: %v", err)
	}
	if !regexp.MustCompile(`^3\.\d+\.\d+$`).MatchString(v) {
		t.Fatalf("SQLiteVersion = %q, want 3.x.y", v)
	}
}
