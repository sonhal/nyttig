package fetcher

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
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
		CREATE TABLE assessors (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL UNIQUE,
			description TEXT,
			color       TEXT,
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE assessments (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id     INTEGER NOT NULL REFERENCES items(id)     ON DELETE CASCADE,
			assessor_id INTEGER NOT NULL REFERENCES assessors(id) ON DELETE CASCADE,
			tag_id      INTEGER          REFERENCES tags(id)      ON DELETE CASCADE,
			score       REAL,
			note        TEXT,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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

	// Verify GUID-based dedup (Atom's <id> element is the GUID).
	first, err := db.GetItem(database, result.NewItems[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	// Atom GUIDs (UUIDs) should be used directly.
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

func TestFetch_ResponseSizeLimit(t *testing.T) {
	database := setupDB(t)
	defer database.Close()

	// Create a feed body larger than 32 MiB
	largeBody := make([]byte, 33*1024*1024) // 33 MiB
	for i := range largeBody {
		largeBody[i] = 'x'
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		w.Write(largeBody)
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Large Feed", srv.URL)

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch returned unexpected error: %v", err)
	}
	if result.FetchError == "" {
		t.Error("expected fetch error for oversized response")
	}
	if !strings.Contains(result.FetchError, "32MiB limit") {
		t.Errorf("expected error to mention 32MiB limit, got: %s", result.FetchError)
	}
	if len(result.NewItems) != 0 {
		t.Errorf("expected 0 new items from oversized response, got %d", len(result.NewItems))
	}

	// Source fetch_error should be updated
	updated, err := db.GetSource(database, src.ID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if updated.FetchError == nil || *updated.FetchError == "" {
		t.Error("expected fetch_error to be set on source")
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
		{"double-encoded ESC is stripped", "hi &#27;[2J&#27;[31mFAKE", "hi [2J[31mFAKE"},
		{"C1 CSI is stripped", "a\u009b31mb", "a31mb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeHTML(c.in); got != c.want {
				t.Errorf("sanitizeHTML(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestParseRSS_StripsControlCharacters(t *testing.T) {
	// U+009B passes XML validation; the description double-encodes ESC so
	// that HTML-unescaping would produce a real ESC.
	feed := []byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>
<item><title>Go 1.27	with
newlines&#155;31m</title><link>http://x/1</link><guid>1</guid>
<description>hi &amp;#27;[2J clear</description><author>a&#155;b</author></item>
</channel></rss>`)
	entries, err := parseRSS(feed)
	if err != nil {
		t.Fatalf("parseRSS: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if want := "Go 1.27 with newlines31m"; e.Title != want {
		t.Errorf("Title = %q, want %q", e.Title, want)
	}
	if want := "hi [2J clear"; e.Description != want {
		t.Errorf("Description = %q, want %q", e.Description, want)
	}
	if want := "ab"; e.Author != want {
		t.Errorf("Author = %q, want %q", e.Author, want)
	}
}

func TestParseDates_NormalizedToUTCWholeSeconds(t *testing.T) {
	want := time.Date(2024, time.June, 15, 8, 30, 0, 0, time.UTC)

	rss := []struct{ name, in string }{
		{"RFC1123Z offset", "Sat, 15 Jun 2024 10:30:00 +0200"},
		{"RFC1123 GMT", "Sat, 15 Jun 2024 08:30:00 GMT"},
		{"RFC822Z", "15 Jun 24 10:30 +0200"},
	}
	for _, tc := range rss {
		t.Run("rss/"+tc.name, func(t *testing.T) {
			got, err := parseDate(tc.in)
			if err != nil {
				t.Fatalf("parseDate: %v", err)
			}
			if got.Location() != time.UTC {
				t.Errorf("location = %v, want UTC", got.Location())
			}
			if !got.Equal(want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}

	atom := []struct{ name, in string }{
		{"offset", "2024-06-15T10:30:00+02:00"},
		{"zulu", "2024-06-15T08:30:00Z"},
		{"fraction", "2024-06-15T08:30:00.75Z"},
		{"fraction with offset", "2024-06-15T10:30:00.999+02:00"},
		{"no zone", "2024-06-15T08:30:00"},
	}
	for _, tc := range atom {
		t.Run("atom/"+tc.name, func(t *testing.T) {
			got, err := parseDate(tc.in)
			if err != nil {
				t.Fatalf("parseDate: %v", err)
			}
			if got.Location() != time.UTC {
				t.Errorf("location = %v, want UTC", got.Location())
			}
			if got.Nanosecond() != 0 {
				t.Errorf("nanoseconds = %d, want 0", got.Nanosecond())
			}
			if !got.Equal(want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// TestParseDate_Variants covers the date shapes real feeds send besides
// the RFC 1123 and RFC 3339 ones above. The first three are copied from the
// feeds (captured 2026-10-03).
func TestParseDate_Variants(t *testing.T) {
	utc := func(y int, mo time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
	}
	cases := []struct {
		name, in string
		want     time.Time
	}{
		{"CISA two-digit year", "Fri, 02 Oct 26 12:00:00 +0000", utc(2026, time.October, 2, 12, 0, 0)},
		{"Cisco no zone, fraction", "2026-10-02 23:18:42.0", utc(2026, time.October, 2, 23, 18, 42)},
		{"Bluesky RSS no weekday or seconds", "26 Sep 2026 11:58 +0000", utc(2026, time.September, 26, 11, 58, 0)},
		{"one-digit day", "Fri, 2 Oct 2026 21:01:00 GMT", utc(2026, time.October, 2, 21, 1, 0)},
		{"no seconds", "Fri, 02 Oct 2026 21:01 +0200", utc(2026, time.October, 2, 19, 1, 0)},
		{"offset with colon", "Fri, 02 Oct 2026 21:01:00 +02:00", utc(2026, time.October, 2, 19, 1, 0)},
		{"UT", "Fri, 02 Oct 2026 21:01:00 UT", utc(2026, time.October, 2, 21, 1, 0)},
		{"EDT is UTC-4", "Fri, 02 Oct 2026 21:01:00 EDT", utc(2026, time.October, 3, 1, 1, 0)},
		{"PST is UTC-8", "Fri, 02 Jan 2026 21:01:00 PST", utc(2026, time.January, 3, 5, 1, 0)},
		{"lower-case zone", "Fri, 02 Oct 2026 21:01:00 gmt", utc(2026, time.October, 2, 21, 1, 0)},
		{"extra spaces", "  Fri,  02 Oct 2026   21:01:00 +0000 ", utc(2026, time.October, 2, 21, 1, 0)},
		{"RFC 822 in an Atom feed", "Fri, 02 Oct 2026 21:01:00 +0000", utc(2026, time.October, 2, 21, 1, 0)},
		{"ISO with a space and zone", "2026-10-02 21:01:00+02:00", utc(2026, time.October, 2, 19, 1, 0)},
		{"date only", "2026-10-02", utc(2026, time.October, 2, 0, 0, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDate(tc.in)
			if err != nil {
				t.Fatalf("parseDate(%q): %v", tc.in, err)
			}
			if got.Location() != time.UTC || got.Nanosecond() != 0 {
				t.Errorf("got %v, want UTC with whole seconds", got)
			}
			if !got.Equal(tc.want) {
				t.Errorf("parseDate(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}

	for _, in := range []string{"", "yesterday", "02/10/2026", "Fri, 32 Oct 2026 21:01:00 +0000"} {
		if got, err := parseDate(in); err == nil {
			t.Errorf("parseDate(%q) = %v, want an error", in, got)
		}
	}
}

// TestFetch_StoresVariantDates fetches an RSS feed with the date shapes
// that used to be dropped and checks every item gets its date, and that an
// unreadable date leaves the item without one.
func TestFetch_StoresVariantDates(t *testing.T) {
	database := setupDB(t)
	defer func() { _ = database.Close() }()

	feed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>t</title>
<item><title>cisa</title><guid>a</guid><pubDate>Fri, 02 Oct 26 12:00:00 +0000</pubDate></item>
<item><title>cisco</title><guid>b</guid><pubDate>2026-10-02 23:18:42.0</pubDate></item>
<item><title>bsky</title><guid>c</guid><pubDate>26 Sep 2026 11:58 +0000</pubDate></item>
<item><title>bad</title><guid>d</guid><pubDate>sometime</pubDate></item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "dates", srv.URL)
	res, err := Fetch(database, src)
	if err != nil || res.FetchError != "" {
		t.Fatalf("Fetch: %v %q", err, res.FetchError)
	}

	want := map[string]string{
		"cisa":  "2026-10-02 12:00:00+00:00",
		"cisco": "2026-10-02 23:18:42+00:00",
		"bsky":  "2026-09-26 11:58:00+00:00",
		"bad":   "",
	}
	rows, err := database.Query(`SELECT title, IFNULL(published, '') FROM items WHERE source_id = ?`, src.ID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var title, published string
		if err := rows.Scan(&title, &published); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[title] = published
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	for title, w := range want {
		if got[title] != w {
			t.Errorf("%s: published = %q, want %q", title, got[title], w)
		}
	}
}

// TestFetch_StoresPublishedInUTC checks the text that reaches the database:
// feeds in different time zones must produce values that sort chronologically
// as text.
func TestFetch_StoresPublishedInUTC(t *testing.T) {
	database := setupDB(t)
	defer func() { _ = database.Close() }()

	feed := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><title>plus two</title><id>a</id><link href="https://example.com/a"/><published>2024-06-15T10:00:00+02:00</published></entry>
  <entry><title>zulu</title><id>b</id><link href="https://example.com/b"/><published>2024-06-15T09:00:00.5Z</published></entry>
</feed>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Zones", srv.URL)
	if res, err := Fetch(database, src); err != nil || res.FetchError != "" {
		t.Fatalf("Fetch: %v / %q", err, res.FetchError)
	}

	// published || '' drops the column type, so the driver returns the stored text.
	rows, err := database.Query(`SELECT title, published || '' FROM items ORDER BY published DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var titles, stored []string
	for rows.Next() {
		var title, pub string
		if err := rows.Scan(&title, &pub); err != nil {
			t.Fatal(err)
		}
		titles = append(titles, title)
		stored = append(stored, pub)
	}
	if got := strings.Join(titles, ","); got != "zulu,plus two" {
		t.Errorf("order = %s, want zulu,plus two (09:00Z is newer than 08:00Z)", got)
	}
	for _, s := range stored {
		if !strings.HasSuffix(s, "+00:00") || strings.Contains(s, ".") {
			t.Errorf("stored published %q is not UTC with whole seconds", s)
		}
	}
}

// TestFetch_InsertFailureIsRecorded: an insert that fails must stay visible
// as the source's fetch_error instead of being cleared by the status update
// that follows the loop.
func TestFetch_InsertFailureIsRecorded(t *testing.T) {
	database := setupDB(t)
	defer func() { _ = database.Close() }()

	// Make every insert into items fail. RAISE(ABORT) is not suppressed by
	// INSERT OR IGNORE.
	if _, err := database.Exec(`CREATE TRIGGER reject_items BEFORE INSERT ON items
		BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	feed := `<?xml version="1.0"?><rss version="2.0"><channel><title>T</title>
<item><title>One</title><link>https://example.com/1</link><guid>1</guid></item>
<item><title>Two</title><link>https://example.com/2</link><guid>2</guid></item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(feed))
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Failing", srv.URL)
	// A stale error from an earlier fetch must be replaced, not kept or cleared.
	if err := db.UpdateSourceFetchError(database, src.ID, "old error"); err != nil {
		t.Fatal(err)
	}

	result, err := Fetch(database, src)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(result.FetchError, "disk full") || !strings.Contains(result.FetchError, "and 1 more") {
		t.Errorf("result.FetchError = %q, want the first insert error and a count", result.FetchError)
	}
	if len(result.NewItems) != 0 {
		t.Errorf("NewItems = %d, want 0", len(result.NewItems))
	}

	updated, err := db.GetSource(database, src.ID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if updated.FetchError == nil || *updated.FetchError != result.FetchError {
		t.Errorf("source fetch_error = %v, want %q", updated.FetchError, result.FetchError)
	}

	// Once inserts work again, the next fetch clears the error.
	if _, err := database.Exec(`DROP TRIGGER reject_items`); err != nil {
		t.Fatal(err)
	}
	result, err = Fetch(database, src)
	if err != nil || result.FetchError != "" || len(result.NewItems) != 2 {
		t.Fatalf("second Fetch = %+v, %v", result, err)
	}
	updated, err = db.GetSource(database, src.ID)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if updated.FetchError != nil && *updated.FetchError != "" {
		t.Errorf("source fetch_error = %q after a clean fetch, want empty", *updated.FetchError)
	}
}

// TestParseFeed_NonUTF8Encodings feeds bodies whose bytes are not UTF-8.
func TestParseFeed_NonUTF8Encodings(t *testing.T) {
	tests := []struct {
		name  string
		body  []byte
		title string
	}{
		{
			name:  "RSS ISO-8859-1",
			body:  []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss version=\"2.0\"><channel><title>T</title><item><title>Caf\xe9 \xc5rhus</title><link>http://x/1</link><guid>1</guid></item></channel></rss>"),
			title: "Café Århus",
		},
		{
			name:  "Atom windows-1252",
			body:  []byte("<?xml version=\"1.0\" encoding=\"windows-1252\"?><feed xmlns=\"http://www.w3.org/2005/Atom\"><entry><title>Caf\xe9 \x93quoted\x94</title><id>1</id><link href=\"http://x/1\"/></entry></feed>"),
			title: "Café “quoted”",
		},
		{
			name:  "RSS UTF-8 still works",
			body:  []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?><rss version=\"2.0\"><channel><title>T</title><item><title>Café</title><link>http://x/1</link><guid>1</guid></item></channel></rss>"),
			title: "Café",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := parseFeed(tc.body)
			if err != nil {
				t.Fatalf("parseFeed: %v", err)
			}
			if len(entries) != 1 || entries[0].Title != tc.title {
				t.Fatalf("entries = %+v, want one titled %q", entries, tc.title)
			}
		})
	}

	if _, err := parseFeed([]byte(`<?xml version="1.0" encoding="no-such-charset"?><rss version="2.0"><channel></channel></rss>`)); err == nil {
		t.Error("an unknown encoding should be an error, not silently accepted")
	}
}

// TestFetch_ISO88591Feed runs a Latin-1 feed through the whole fetch path.
func TestFetch_ISO88591Feed(t *testing.T) {
	database := setupDB(t)
	defer func() { _ = database.Close() }()

	body := []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss version=\"2.0\"><channel><title>T</title><item><title>Caf\xe9</title><link>http://x/1</link><guid>1</guid></item></channel></rss>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml; charset=ISO-8859-1")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := insertTestSource(t, database, "Latin", srv.URL)
	result, err := Fetch(database, src)
	if err != nil || result.FetchError != "" {
		t.Fatalf("Fetch = %v, %q", err, result.FetchError)
	}
	if len(result.NewItems) != 1 || result.NewItems[0].Title != "Café" {
		t.Fatalf("NewItems = %+v, want title Café", result.NewItems)
	}
	item, err := db.GetItem(database, result.NewItems[0].ID)
	if err != nil || item.Title != "Café" {
		t.Errorf("stored title = %v (%v), want Café", item, err)
	}
}
