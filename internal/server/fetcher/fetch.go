// Package fetcher handles fetching RSS/Atom feeds from remote sources,
// parsing them, deduplicating entries, and inserting new items into the
// database.
package fetcher

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sonhal/nyttig/internal/server/db"
)

// userAgent is the standard User-Agent string sent with HTTP requests.
const userAgent = "Nyttig/0.1 (news-aggregator)"

// httpClient is the HTTP client with timeout for feed fetching.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// ── RSS 2.0 types ───────────────────────────────────────────────

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Author      string `xml:"author"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
}

// ── Atom 1.0 types ──────────────────────────────────────────────

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string      `xml:"title"`
	Links     []atomLink  `xml:"link"`
	ID        string      `xml:"id"`
	Updated   string      `xml:"updated"`
	Summary   string      `xml:"summary"`
	Content   string      `xml:"content"`
	Author    *atomAuthor `xml:"author"`
	Published string      `xml:"published"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

// ── Parsed entry ────────────────────────────────────────────────

// parsedEntry is a normalized feed entry regardless of format.
type parsedEntry struct {
	Title       string
	Link        string
	Description string
	Author      string
	GUID        string
	Published   *time.Time
}

// ── FetchResult ─────────────────────────────────────────────────

// FetchResult describes the outcome of a single feed fetch.
type FetchResult struct {
	SourceID   int64
	NewItems   []*db.Item
	FetchError string
}

// ── Public API ──────────────────────────────────────────────────

// Fetch fetches the feed from the source's URL, parses it as RSS 2.0 or
// Atom, deduplicates entries via GUID or SHA-256 of the link, and inserts
// new items into the database. It updates the source's last_fetch and
// fetch_error columns before returning.
func Fetch(database *sql.DB, src *db.Source) (*FetchResult, error) {
	return fetchInternal(database, src, httpClient)
}

// FetchWithClient is like Fetch but accepts a custom HTTP client (useful
// for testing with httptest.NewServer).
func FetchWithClient(database *sql.DB, src *db.Source, client doer) (*FetchResult, error) {
	return fetchInternal(database, src, client)
}

// doer is a minimal HTTP-client interface, useful for testing.
type doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ── Internal ────────────────────────────────────────────────────

func fetchInternal(database *sql.DB, src *db.Source, client doer) (*FetchResult, error) {
	result := &FetchResult{SourceID: src.ID}

	// 1. Build HTTP request.
	req, err := http.NewRequest(http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, */*")

	// 2. Issue HTTP GET.
	resp, err := client.Do(req)
	if err != nil {
		result.FetchError = err.Error()
		_ = db.UpdateSourceFetchError(database, src.ID, err.Error())
		_ = db.UpdateSourceLastFetch(database, src.ID, time.Now())
		return result, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("HTTP %d", resp.StatusCode)
		result.FetchError = err.Error()
		_ = db.UpdateSourceFetchError(database, src.ID, err.Error())
		_ = db.UpdateSourceLastFetch(database, src.ID, time.Now())
		return result, nil
	}

	// 3. Read response body with size limit.
	const maxBodySize = 10 * 1024 * 1024 // 10 MiB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		result.FetchError = err.Error()
		_ = db.UpdateSourceFetchError(database, src.ID, err.Error())
		_ = db.UpdateSourceLastFetch(database, src.ID, time.Now())
		return result, nil
	}
	if int64(len(body)) > maxBodySize {
		err := fmt.Errorf("response body exceeds 10MiB limit")
		result.FetchError = err.Error()
		_ = db.UpdateSourceFetchError(database, src.ID, err.Error())
		_ = db.UpdateSourceLastFetch(database, src.ID, time.Now())
		return result, nil
	}

	// 4. Parse entries (auto-detect RSS vs Atom by root element).
	entries, parseErr := parseFeed(body)
	if parseErr != nil {
		result.FetchError = parseErr.Error()
		_ = db.UpdateSourceFetchError(database, src.ID, parseErr.Error())
		_ = db.UpdateSourceLastFetch(database, src.ID, time.Now())
		return result, nil
	}

	// 5. Insert new items with deduplication.
	for _, entry := range entries {
		if entry.GUID == "" && entry.Link == "" {
			continue
		}

		dedupKey := DedupKey(entry.GUID, entry.Link)

		item := &db.Item{
			SourceID: src.ID,
			GUID:     dedupKey,
			Link:     entry.Link,
			Title:    entry.Title,
		}

		if entry.Description != "" {
			d := entry.Description
			item.Description = &d
		}
		if entry.Author != "" {
			a := entry.Author
			item.Author = &a
		}
		if entry.Published != nil {
			item.Published = entry.Published
		}

		id, inserted, err := db.InsertItem(database, item)
		if err != nil {
			// Log but continue processing remaining entries.
			result.FetchError = fmt.Sprintf("insert item %q: %v", dedupKey, err)
			continue
		}
		if inserted {
			item.ID = id
			result.NewItems = append(result.NewItems, item)
		}
	}

	// 6. Update source fetch status.
	_ = db.UpdateSourceLastFetch(database, src.ID, time.Now())
	_ = db.UpdateSourceFetchError(database, src.ID, "")

	return result, nil
}

// parseFeed detects the root XML element and dispatches to the appropriate
// parser. Returns normalized parsedEntry values.
func parseFeed(body []byte) ([]parsedEntry, error) {
	s := strings.TrimSpace(string(body))

	// Auto-detect format by scanning for the root opening tag.
	if strings.HasPrefix(s, "<?xml") {
		// Skip XML declaration.
		idx := strings.Index(s, ">")
		if idx >= 0 {
			s = s[idx+1:]
		}
	}
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "<rss") || strings.HasPrefix(s, "<rss ") {
		return parseRSS(body)
	}
	if strings.HasPrefix(s, "<feed") || strings.HasPrefix(s, "<feed ") {
		return parseAtom(body)
	}

	// Try both parsers as a fallback.
	if entries, err := parseRSS(body); err == nil && len(entries) > 0 {
		return entries, nil
	}
	if entries, err := parseAtom(body); err == nil && len(entries) > 0 {
		return entries, nil
	}

	return nil, fmt.Errorf("unrecognized feed format")
}

// parseRSS parses an RSS 2.0 XML feed into normalized entries.
func parseRSS(body []byte) ([]parsedEntry, error) {
	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("rss parse: %w", err)
	}

	var entries []parsedEntry
	for _, item := range feed.Channel.Items {
		entry := parsedEntry{
			Title:       strings.TrimSpace(item.Title),
			Link:        strings.TrimSpace(item.Link),
			Description: sanitizeHTML(item.Description),
			Author:      strings.TrimSpace(item.Author),
			GUID:        strings.TrimSpace(item.GUID),
		}
		if item.PubDate != "" {
			if t, err := parseRSSDate(item.PubDate); err == nil {
				entry.Published = &t
			}
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// parseAtom parses an Atom 1.0 XML feed into normalized entries.
func parseAtom(body []byte) ([]parsedEntry, error) {
	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("atom parse: %w", err)
	}

	var entries []parsedEntry
	for _, e := range feed.Entries {
		entry := parsedEntry{
			Title: strings.TrimSpace(e.Title),
			GUID:  strings.TrimSpace(e.ID),
		}

		// Extract link: prefer alternate, fallback to first link.
		for _, l := range e.Links {
			if l.Rel == "alternate" || entry.Link == "" {
				entry.Link = strings.TrimSpace(l.Href)
			}
			if l.Rel == "alternate" {
				break
			}
		}

		// Description: use summary, fallback to content.
		if s := sanitizeHTML(e.Summary); s != "" {
			entry.Description = s
		} else if c := sanitizeHTML(e.Content); c != "" {
			entry.Description = c
		}

		if e.Author != nil {
			entry.Author = strings.TrimSpace(e.Author.Name)
		}

		// Date: prefer published, fallback to updated.
		dateStr := e.Published
		if dateStr == "" {
			dateStr = e.Updated
		}
		if dateStr != "" {
			if t, err := parseAtomDate(dateStr); err == nil {
				entry.Published = &t
			}
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// ── Date parsing ────────────────────────────────────────────────

// rssDateFormats are the formats commonly used in RSS pubDate elements.
var rssDateFormats = []string{
	time.RFC1123Z, // Mon, 02 Jan 2006 15:04:05 -0700
	time.RFC1123,  // Mon, 02 Jan 2006 15:04:05 MST
	time.RFC822Z,  // 02 Jan 06 15:04 -0700
	time.RFC822,   // 02 Jan 06 15:04 MST
	"Mon, 02 Jan 2006 15:04:05 GMT",
	"Mon, 2 Jan 2006 15:04:05 -0700",
}

func parseRSSDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range rssDateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized RSS date %q", s)
}

// atomDateFormats are the formats commonly used in Atom date elements.
var atomDateFormats = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02",
}

func parseAtomDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range atomDateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized Atom date %q", s)
}

// ── HTML sanitization ───────────────────────────────────────────

// htmlTagRE matches HTML/XML tags (e.g. <p>, </a>, <a href="...">).
var htmlTagRE = regexp.MustCompile(`<[^>]*>`)

// sanitizeHTML turns an HTML description fragment into plain, readable text:
// it removes tags, decodes HTML entities, and collapses runs of whitespace
// into single spaces. Feed descriptions are frequently wrapped in markup
// (e.g. "<p><a href=...>") which is noise in a compact terminal list.
func sanitizeHTML(s string) string {
	s = htmlTagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}
