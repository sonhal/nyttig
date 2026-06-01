package fetcher

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sonhal/nyttig/internal/server/db"
)

// setupDB creates an in-memory SQLite database with the full schema
// (including FTS triggers) for integration testing.
func setupDB(t *testing.T) *sql.DB {
	t.Helper()

	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("pragma foreign_keys: %v", err)
	}

	// Create schema manually (db.Open runs migrations but requires files on disk).
	schema := `
		CREATE TABLE sources (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL,
			url         TEXT NOT NULL UNIQUE,
			type        TEXT NOT NULL DEFAULT 'rss',
			refresh_sec INTEGER NOT NULL DEFAULT 3600,
			enabled     BOOLEAN NOT NULL DEFAULT 1,
			color       TEXT,
			abbreviation TEXT,
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_fetch  DATETIME,
			fetch_error TEXT
		);
		CREATE TABLE items (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id   INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
			guid        TEXT NOT NULL,
			link        TEXT NOT NULL,
			title       TEXT NOT NULL,
			description TEXT,
			author      TEXT,
			published   DATETIME,
			fetched_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(source_id, guid)
		);
		CREATE TABLE tags (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			name    TEXT NOT NULL UNIQUE,
			color   TEXT
		);
		CREATE TABLE item_tags (
			item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
			tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
			PRIMARY KEY (item_id, tag_id)
		);
		CREATE TABLE view_state (
			item_id   INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
			viewed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`
	if _, err := database.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	return database
}

func insertTestSource(t *testing.T, database *sql.DB, name, url string) *db.Source {
	t.Helper()
	src := &db.Source{
		Name:       name,
		URL:        url,
		Type:       "rss",
		RefreshSec: 3600,
		Enabled:    true,
	}
	id, err := db.InsertSource(database, src)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	src.ID = id
	return src
}

// countItems returns the number of items in the database.
func countItems(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM items").Scan(&count); err != nil {
		t.Fatalf("count items: %v", err)
	}
	return count
}

func TestFetch_RSS_Feed(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	// Sample RSS 2.0 feed.
	rssFeed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Blog</title>
    <link>https://example.com/</link>
    <description>A test feed</description>
    <item>
      <title>First Post</title>
      <link>https://example.com/first</link>
      <guid isPermaLink="true">https://example.com/first</guid>
      <description>This is the first post.</description>
      <author>Alice</author>
      <pubDate>Mon, 01 Jan 2024 12:00:00 GMT</pubDate>
    </item>
    <item>
      <title>Second Post</title>
      <link>https://example.com/second</link>
      <guid isPermaLink="true">https://example.com/second</guid>
      <description>This is the second post.</description>
      <author>Bob</author>
      <pubDate>Tue, 02 Jan 2024 12:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(rssFeed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Test Blog", srv.URL)

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result.FetchError != "" {
		t.Fatalf("unexpected fetch error: %s", result.FetchError)
	}

	if len(result.NewItems) != 2 {
		t.Fatalf("expected 2 new items, got %d", len(result.NewItems))
	}

	// Verify items in DB.
	if n := countItems(t, database); n != 2 {
		t.Fatalf("expected 2 items in DB, got %d", n)
	}

	// Verify item details.
	item, err := db.GetItem(database, result.NewItems[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item == nil {
		t.Fatal("item not found in DB")
	}
	if item.Title != "First Post" {
		t.Errorf("expected title 'First Post', got %q", item.Title)
	}
	if item.Link != "https://example.com/first" {
		t.Errorf("expected link, got %q", item.Link)
	}
	if item.Author == nil || *item.Author != "Alice" {
		t.Errorf("expected author 'Alice', got %v", item.Author)
	}
	if item.SourceID != src.ID {
		t.Errorf("expected source_id %d, got %d", src.ID, item.SourceID)
	}

	// Verify source last_fetch was updated.
	updated, err := db.GetSource(database, src.ID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if updated.LastFetch == nil {
		t.Error("expected last_fetch to be set")
	}
}

func TestFetch_Atom_Feed(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	// Sample Atom 1.0 feed.
	atomFeed := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Test Blog</title>
  <link href="https://example.com/atom" rel="self"/>
  <updated>2024-01-15T12:00:00Z</updated>
  <entry>
    <title>Atom First Post</title>
    <link href="https://example.com/atom/first"/>
    <id>urn:uuid:1225c695-cfb8-4ebb-aaaa-80da344efa6a</id>
    <updated>2024-01-10T08:00:00Z</updated>
    <summary>First post summary</summary>
    <author>
      <name>Charlie</name>
    </author>
  </entry>
  <entry>
    <title>Atom Second Post</title>
    <link href="https://example.com/atom/second"/>
    <id>urn:uuid:2225c695-cfb8-4ebb-bbbb-80da344efa6b</id>
    <updated>2024-01-11T08:00:00Z</updated>
    <summary>Second post summary</summary>
    <author>
      <name>Dana</name>
    </author>
  </entry>
</feed>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(atomFeed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Atom Test", srv.URL)

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result.FetchError != "" {
		t.Fatalf("unexpected fetch error: %s", result.FetchError)
	}

	if len(result.NewItems) != 2 {
		t.Fatalf("expected 2 new items, got %d", len(result.NewItems))
	}

	if n := countItems(t, database); n != 2 {
		t.Fatalf("expected 2 items in DB, got %d", n)
	}

	// Verify GUID-based dedup (Atom uses <id> element which gofeed maps to GUID).
	first, err := db.GetItem(database, result.NewItems[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	// Atom GUIDs (UUIDs) should be used directly by gofeed.
	if first.GUID == "" {
		t.Error("expected non-empty GUID from Atom id element")
	}
}

func TestFetch_Deduplication(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	rssFeed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Dedup Feed</title>
    <link>https://example.com/</link>
    <description>A dedup test</description>
    <item>
      <title>Post One</title>
      <link>https://example.com/one</link>
      <guid>guid-123</guid>
    </item>
  </channel>
</rss>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(rssFeed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Dedup Feed", srv.URL)

	// First fetch: should insert 1 item.
	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if len(result.NewItems) != 1 {
		t.Fatalf("expected 1 new item, got %d", len(result.NewItems))
	}
	if n := countItems(t, database); n != 1 {
		t.Fatalf("expected 1 item in DB, got %d", n)
	}

	// Second fetch: same feed, should insert 0 items (all deduplicated).
	result, err = Fetch(database, src)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if len(result.NewItems) != 0 {
		t.Fatalf("expected 0 new items (dedup), got %d", len(result.NewItems))
	}
	if n := countItems(t, database); n != 1 {
		t.Fatalf("expected still 1 item in DB, got %d", n)
	}
}

func TestFetch_MixedNewAndExisting(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	// Feed with 1 item.
	rssFeed1 := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Mixed Feed</title>
    <link>https://example.com/</link>
    <item>
      <title>Existing Post</title>
      <link>https://example.com/existing</link>
      <guid>guid-existing</guid>
    </item>
  </channel>
</rss>`

	// Feed with 2 items (1 existing, 1 new).
	rssFeed2 := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Mixed Feed</title>
    <link>https://example.com/</link>
    <item>
      <title>Existing Post</title>
      <link>https://example.com/existing</link>
      <guid>guid-existing</guid>
    </item>
    <item>
      <title>New Post</title>
      <link>https://example.com/new</link>
      <guid>guid-new</guid>
    </item>
  </channel>
</rss>`

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		if callCount == 1 {
			w.Write([]byte(rssFeed1))
		} else {
			w.Write([]byte(rssFeed2))
		}
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Mixed Feed", srv.URL)

	// First fetch: 1 new item.
	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if len(result.NewItems) != 1 {
		t.Fatalf("expected 1 new item, got %d", len(result.NewItems))
	}

	// Second fetch: 1 new, 1 existing -> only 1 new inserted.
	result, err = Fetch(database, src)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if len(result.NewItems) != 1 {
		t.Fatalf("expected 1 new item on second fetch, got %d", len(result.NewItems))
	}
	if n := countItems(t, database); n != 2 {
		t.Fatalf("expected 2 items in DB total, got %d", n)
	}
}

func TestFetch_NoGuidFallbackToLinkHash(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	// RSS feed without <guid> elements.
	rssFeed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>No GUID Feed</title>
    <link>https://example.com/</link>
    <item>
      <title>Link Only Post</title>
      <link>https://example.com/link-only</link>
      <description>No GUID here</description>
    </item>
  </channel>
</rss>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(rssFeed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "No GUID Feed", srv.URL)

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(result.NewItems) != 1 {
		t.Fatalf("expected 1 new item, got %d", len(result.NewItems))
	}

	// The GUID should be the SHA-256 of the link.
	expectedGUID := DedupKey("", "https://example.com/link-only")
	item, err := db.GetItem(database, result.NewItems[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.GUID != expectedGUID {
		t.Errorf("expected GUID %q (SHA-256 of link), got %q", expectedGUID, item.GUID)
	}

	// Second fetch with same link: dedup via link hash.
	result, err = Fetch(database, src)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if len(result.NewItems) != 0 {
		t.Fatalf("expected 0 new items (dedup by link hash), got %d", len(result.NewItems))
	}
}

func TestFetch_HTTPErrorResponse(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Error Feed", srv.URL)

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch returned unexpected error: %v", err)
	}
	if result.FetchError == "" {
		t.Error("expected fetch error for HTTP 500")
	}
	if len(result.NewItems) != 0 {
		t.Errorf("expected 0 new items, got %d", len(result.NewItems))
	}

	// Source fetch_error should be updated.
	updated, err := db.GetSource(database, src.ID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if updated.FetchError == nil || *updated.FetchError == "" {
		t.Error("expected fetch_error to be set on source")
	}
}

func TestFetch_InvalidXML(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("this is not XML at all"))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Bad XML Feed", srv.URL)

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch returned unexpected error: %v", err)
	}
	if result.FetchError == "" {
		t.Error("expected fetch error for invalid XML")
	}
	if len(result.NewItems) != 0 {
		t.Errorf("expected 0 new items, got %d", len(result.NewItems))
	}
}

func TestFetch_FetchWithClient(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	rssFeed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Custom Client Feed</title>
    <link>https://example.com/</link>
    <item>
      <title>Custom Client Post</title>
      <link>https://example.com/custom</link>
      <guid>custom-guid</guid>
    </item>
  </channel>
</rss>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(rssFeed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Custom Client Feed", srv.URL)

	result, err := FetchWithClient(database, src, http.DefaultClient)
	if err != nil {
		t.Fatalf("FetchWithClient: %v", err)
	}
	if result.FetchError != "" {
		t.Fatalf("unexpected fetch error: %s", result.FetchError)
	}
	if len(result.NewItems) != 1 {
		t.Fatalf("expected 1 new item, got %d", len(result.NewItems))
	}
}

func TestFetch_EmptyFeed(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	rssFeed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Empty Feed</title>
    <link>https://example.com/</link>
    <description>No items here</description>
  </channel>
</rss>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(rssFeed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Empty Feed", srv.URL)

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result.FetchError != "" {
		t.Fatalf("unexpected fetch error: %s", result.FetchError)
	}
	if len(result.NewItems) != 0 {
		t.Errorf("expected 0 new items from empty feed, got %d", len(result.NewItems))
	}

	// Source should still have last_fetch updated.
	updated, err := db.GetSource(database, src.ID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if updated.LastFetch == nil {
		t.Error("expected last_fetch to be set even for empty feeds")
	}
}

func TestFetch_TimeFields(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	rssFeed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Time Feed</title>
    <link>https://example.com/</link>
    <item>
      <title>Timed Post</title>
      <link>https://example.com/timed</link>
      <guid>time-guid</guid>
      <pubDate>Sat, 15 Jun 2024 14:30:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(rssFeed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Time Feed", srv.URL)

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result.FetchError != "" {
		t.Fatalf("unexpected fetch error: %s", result.FetchError)
	}

	item, err := db.GetItem(database, result.NewItems[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Published == nil {
		t.Error("expected published to be set")
	} else {
		expected := time.Date(2024, time.June, 15, 14, 30, 0, 0, time.UTC)
		if !item.Published.Equal(expected) {
			t.Errorf("expected %v, got %v", expected, *item.Published)
		}
	}
	if item.FetchedAt.IsZero() {
		t.Error("expected fetched_at to be non-zero")
	}
}

func TestFetch_DescriptionField(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	rssFeed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Desc Feed</title>
    <link>https://example.com/</link>
    <item>
      <title>Post with Desc</title>
      <link>https://example.com/desc</link>
      <guid>desc-guid</guid>
      <description>This is a description with &lt;b&gt;HTML&lt;/b&gt; tags.</description>
    </item>
  </channel>
</rss>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(rssFeed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Desc Feed", srv.URL)

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	item, err := db.GetItem(database, result.NewItems[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Description == nil {
		t.Error("expected description to be set")
	} else {
		expected := "This is a description with HTML tags."
		if *item.Description != expected {
			t.Errorf("expected description %q, got %q", expected, *item.Description)
		}
	}
}

func TestSanitizeHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Just text", "Just text"},
		{"strips tags", `<p><a href="https://news.ycombinator.com/item?id=1">Comments</a></p>`, "Comments"},
		{"decodes entities", "Tom &amp; Jerry &lt;3", "Tom & Jerry <3"},
		{"collapses whitespace", "lots\n\n  of   space", "lots of space"},
		{"tags only", `<p><br/></p>`, ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeHTML(c.in); got != c.want {
				t.Errorf("sanitizeHTML(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
