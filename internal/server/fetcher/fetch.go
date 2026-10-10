// Package fetcher handles fetching RSS, Atom and JSON feeds and Bluesky accounts from
// remote sources, parsing them, deduplicating entries, and inserting new
// items into the database.
package fetcher

import (
	"bytes"
	"database/sql"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html/charset"

	"github.com/sonhal/nyttig/internal/server/db"
)

// userAgent is the standard User-Agent string sent with HTTP requests.
const userAgent = "Nyttig/0.1 (news-aggregator)"

// httpClient is the default HTTP client for Fetch: 30s timeout, no address
// restrictions. The daemon builds its own with NewHTTPClient and calls
// FetchWithClient.
var httpClient = NewHTTPClient(ClientOptions{})

// ── RSS 2.0 types ───────────────────────────────────────────────

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	Author      string   `xml:"author"`
	Creators    []string `xml:"http://purl.org/dc/elements/1.1/ creator"`
	GUID        string   `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	// Comments also matches slash:comments, a comment count that WordPress
	// feeds send next to the URL; commentsURL picks the URL.
	Comments []string `xml:"comments"`
}

// ── RSS 1.0 / 0.90 (RDF) types ──────────────────────────────────

type rdfFeed struct {
	XMLName xml.Name `xml:"http://www.w3.org/1999/02/22-rdf-syntax-ns# RDF"`
	// Items are siblings of <channel>, not inside it. "item" without a
	// namespace matches both RSS 1.0's and RSS 0.90's.
	Items []rdfItem `xml:"item"`
}

type rdfItem struct {
	About       string   `xml:"http://www.w3.org/1999/02/22-rdf-syntax-ns# about,attr"`
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	Date        string   `xml:"http://purl.org/dc/elements/1.1/ date"`
	Creators    []string `xml:"http://purl.org/dc/elements/1.1/ creator"`
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
	// BadDate is the feed's date when it could not be parsed, for the log.
	BadDate string
	// Assessments are scores the source itself gives the entry (EUVD),
	// written on every fetch when they change.
	Assessments []entryAssessment
}

// entryAssessment is a whole-item score from the source, by assessor name.
type entryAssessment struct {
	Assessor string
	Score    float64 // 0 to 1
	Note     string
}

// entryDate parses an entry's date. It returns the raw string as bad when
// there is one and it doesn't parse.
func entryDate(s string) (*time.Time, string) {
	if strings.TrimSpace(s) == "" {
		return nil, ""
	}
	t, err := parseDate(s)
	if err != nil {
		return nil, strings.TrimSpace(s)
	}
	return &t, ""
}

// ── FetchResult ─────────────────────────────────────────────────

// FetchResult describes the outcome of a single feed fetch.
type FetchResult struct {
	SourceID int64
	NewItems []*db.Item
	// UpdatedItemIDs are items that existed before this fetch and whose
	// assessments it changed (EUVD scores), for the daemon to push.
	UpdatedItemIDs []int64
	FetchError     string
}

// ── Public API ──────────────────────────────────────────────────

// Fetch fetches the feed from the source's URL, parses it as RSS 2.0 or
// Atom (or, for a Bluesky source, reads the account's posts through the
// Bluesky API), deduplicates entries via GUID or SHA-256 of the link, and inserts
// new items into the database. It updates the source's last_fetch and
// fetch_error columns before returning.
func Fetch(database *sql.DB, src *db.Source) (*FetchResult, error) {
	return fetchInternal(database, src, httpClient)
}

// FetchWithClient is like Fetch but accepts a custom HTTP client, such as one
// from NewHTTPClient or one for testing with httptest.NewServer.
func FetchWithClient(database *sql.DB, src *db.Source, client doer) (*FetchResult, error) {
	return fetchInternal(database, src, client)
}

// doer is a minimal HTTP-client interface, useful for testing.
type doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ── Internal ────────────────────────────────────────────────────

// buildRequest returns the GET request for a source: its URL for RSS and
// Atom, the author-feed API call for Bluesky. An error means the source is
// misconfigured; it is reported as the source's fetch error.
func buildRequest(src *db.Source) (*http.Request, error) {
	reqURL, accept := src.URL, "application/rss+xml, application/atom+xml, application/feed+json, application/rdf+xml, application/xml, */*"
	if src.Type == TypeBluesky {
		// Config-seeded sources skip the service's validation, so check
		// the account here.
		var err error
		if reqURL, err = blueskyFeedURL(src.URL); err != nil {
			return nil, err
		}
		accept = "application/json"
	}
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	return req, nil
}

// parseBody parses a response body according to the source's type.
func parseBody(src *db.Source, body []byte) ([]parsedEntry, error) {
	if src.Type == TypeBluesky {
		return parseBluesky(body)
	}
	// Detect the format from the document.
	return parseFeed(body)
}

// maxBodySize caps a response body.
const maxBodySize = 32 * 1024 * 1024 // 32 MiB

// getBody issues req and reads the body of a 200 answer, at most
// maxBodySize. Any other status is an error (Bluesky's carries its message).
func getBody(client doer, req *http.Request, typ string) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		if typ == TypeBluesky {
			return nil, xrpcError(resp)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBodySize {
		return nil, fmt.Errorf("response body exceeds 32MiB limit")
	}
	return body, nil
}

// applyAssessments writes an entry's assessments of item itemID, skipping
// those already stored with the same score and note. It reports whether
// it wrote any. assessorIDs caches the assessors' IDs by name for one fetch.
func applyAssessments(database *sql.DB, assessorIDs map[string]int64, itemID int64, list []entryAssessment) (bool, error) {
	changed := false
	for _, a := range list {
		assessorID, ok := assessorIDs[a.Assessor]
		if !ok {
			var err error
			if assessorID, err = db.EnsureAssessor(database, a.Assessor, euvdAssessorDescriptions[a.Assessor]); err != nil {
				return changed, fmt.Errorf("assessor %q: %w", a.Assessor, err)
			}
			assessorIDs[a.Assessor] = assessorID
		}
		stored, err := db.GetAssessment(database, itemID, assessorID, 0)
		if err != nil {
			return changed, err
		}
		if stored != nil && stored.Score != nil && *stored.Score == a.Score && stored.Note == a.Note {
			continue
		}
		score := a.Score
		if _, err := db.PutAssessment(database, &db.Assessment{ItemID: itemID, AssessorID: assessorID, Score: &score, Note: a.Note}); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

func fetchInternal(database *sql.DB, src *db.Source, client doer) (*FetchResult, error) {
	result := &FetchResult{SourceID: src.ID}

	// fail records a fetch error on the source and the result.
	fail := func(err error) (*FetchResult, error) {
		result.FetchError = err.Error()
		_ = db.UpdateSourceFetchError(database, src.ID, err.Error())
		_ = db.UpdateSourceLastFetch(database, src.ID, time.Now())
		return result, nil
	}

	// 1. Fetch and parse the entries.
	var entries []parsedEntry
	if src.Type == TypeEUVD {
		var note string
		var err error
		if entries, note, err = fetchEUVD(client, src); err != nil {
			return fail(err)
		}
		result.FetchError = note
	} else {
		req, err := buildRequest(src)
		if err != nil {
			return fail(err)
		}
		body, err := getBody(client, req, src.Type)
		if err != nil {
			return fail(err)
		}
		if entries, err = parseBody(src, body); err != nil {
			return fail(err)
		}
	}

	// 2. Insert new items with deduplication.
	var insertErrs []string
	badDates, badDate := 0, ""
	assessorIDs := map[string]int64{}
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
			// Keep going so one bad entry doesn't drop the rest; the
			// failure is reported through the source's fetch_error below.
			insertErrs = append(insertErrs, fmt.Sprintf("insert item %q: %v", dedupKey, err))
			continue
		}
		if len(entry.Assessments) > 0 {
			if !inserted {
				// INSERT OR IGNORE reports no id for an existing row.
				if id, err = db.ItemIDByGUID(database, src.ID, dedupKey); err != nil {
					insertErrs = append(insertErrs, fmt.Sprintf("look up item %q: %v", dedupKey, err))
					continue
				}
			}
			changed, err := applyAssessments(database, assessorIDs, id, entry.Assessments)
			if err != nil {
				insertErrs = append(insertErrs, fmt.Sprintf("assess item %q: %v", dedupKey, err))
			} else if changed && !inserted && id != 0 {
				result.UpdatedItemIDs = append(result.UpdatedItemIDs, id)
			}
		}
		if inserted {
			item.ID = id
			result.NewItems = append(result.NewItems, item)
			if entry.BadDate != "" {
				badDates++
				badDate = entry.BadDate
			}
		}
	}
	// Only new items count, so a feed's unreadable dates are logged once.
	if badDates > 0 {
		slog.Warn("unrecognized item date", "source_id", src.ID, "items", badDates, "date", badDate)
	}

	if len(insertErrs) > 0 {
		result.FetchError = insertErrs[0]
		if len(insertErrs) > 1 {
			result.FetchError += fmt.Sprintf(" (and %d more)", len(insertErrs)-1)
		}
	}

	// 3. Update source fetch status. FetchError is empty when every entry
	// was handled, which clears an earlier error.
	_ = db.UpdateSourceLastFetch(database, src.ID, time.Now())
	_ = db.UpdateSourceFetchError(database, src.ID, result.FetchError)

	return result, nil
}

// parseFeed detects the feed format (see feedKind) and dispatches to its
// parser. Returns normalized parsedEntry values.
func parseFeed(body []byte) ([]parsedEntry, error) {
	body = bytes.TrimPrefix(body, utf8BOM)
	switch feedKind(body) {
	case kindRSS:
		return parseRSS(body)
	case kindAtom:
		return parseAtom(body)
	case kindRDF:
		return parseRDF(body)
	case kindJSON:
		return parseJSONFeed(body)
	}
	return nil, fmt.Errorf("unrecognized feed format")
}

// utf8BOM is the byte order mark some feeds start with.
var utf8BOM = []byte("\xef\xbb\xbf")

// Feed formats feedKind recognizes.
const (
	kindRSS  = "rss"  // RSS 2.0 (and 0.9x): <rss>
	kindAtom = "atom" // Atom 1.0: <feed>
	kindRDF  = "rdf"  // RSS 1.0 and 0.90: <rdf:RDF>
	kindJSON = "json" // JSON Feed 1.x
)

// feedKind tells the format of a body without parsing all of it: a JSON
// object is a JSON Feed, and XML is decided by its root element's local
// name, read with a token scan that skips the declaration, comments, a
// doctype and whitespace. "" means neither.
func feedKind(body []byte) string {
	body = bytes.TrimPrefix(body, utf8BOM)
	if trimmed := bytes.TrimLeftFunc(body, unicode.IsSpace); len(trimmed) > 0 && trimmed[0] == '{' {
		return kindJSON
	}
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.CharsetReader = charset.NewReaderLabel
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "rss":
			return kindRSS
		case "feed":
			return kindAtom
		case "RDF":
			return kindRDF
		}
		return ""
	}
}

// decodeXML is xml.Unmarshal with support for the encoding named in the XML
// declaration. Plain Unmarshal fails on anything but UTF-8 ("encoding
// ISO-8859-1 declared but Decoder.CharsetReader is nil"), and plenty of
// feeds still declare ISO-8859-1 or windows-1252.
func decodeXML(body []byte, v any) error {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.CharsetReader = charset.NewReaderLabel
	return dec.Decode(v)
}

// parseRSS parses an RSS 2.0 XML feed into normalized entries.
func parseRSS(body []byte) ([]parsedEntry, error) {
	var feed rssFeed
	if err := decodeXML(body, &feed); err != nil {
		return nil, fmt.Errorf("rss parse: %w", err)
	}

	var entries []parsedEntry
	for _, item := range feed.Channel.Items {
		entry := parsedEntry{
			Title:       cleanText(item.Title),
			Link:        strings.TrimSpace(item.Link),
			Description: sanitizeHTML(item.Description),
			Author:      joinNames(item.Creators),
			GUID:        strings.TrimSpace(item.GUID),
		}
		// dc:creator is a name; RSS 2.0's <author> is an e-mail address.
		if entry.Author == "" {
			entry.Author = cleanText(item.Author)
		}
		addComments(&entry, item.Comments)
		entry.Published, entry.BadDate = entryDate(item.PubDate)
		entries = append(entries, entry)
	}

	return entries, nil
}

// parseRDF parses an RSS 1.0 or 0.90 (RDF) feed into normalized entries.
func parseRDF(body []byte) ([]parsedEntry, error) {
	var feed rdfFeed
	if err := decodeXML(body, &feed); err != nil {
		return nil, fmt.Errorf("rss 1.0 parse: %w", err)
	}

	var entries []parsedEntry
	for _, item := range feed.Items {
		entry := parsedEntry{
			Title:       cleanText(item.Title),
			Link:        strings.TrimSpace(item.Link),
			Description: sanitizeHTML(item.Description),
			Author:      joinNames(item.Creators),
			GUID:        strings.TrimSpace(item.About),
		}
		entry.Published, entry.BadDate = entryDate(item.Date)
		entries = append(entries, entry)
	}

	return entries, nil
}

// ── Shared entry helpers ────────────────────────────────────────

// joinNames joins author names (dc:creator elements, JSON Feed authors)
// with ", ", leaving out blank ones.
func joinNames(names []string) string {
	var out []string
	for _, n := range names {
		if n = cleanText(n); n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

// addComments records an RSS item's <comments> URL, the discussion thread
// on sites like Hacker News and Lobsters. Items have one link, so the
// article stays the link and the thread is appended to the description,
// unless the description already has it; an item without a link links to
// the thread instead.
func addComments(e *parsedEntry, comments []string) {
	c := commentsURL(comments)
	switch {
	case c == "" || c == e.Link:
	case e.Link == "":
		e.Link = c
	case strings.Contains(e.Description, c):
	case e.Description == "":
		e.Description = "Comments: " + c
	default:
		e.Description += " Comments: " + c
	}
}

// commentsURL returns the first value that is an absolute http(s) URL, so
// slash:comments counts and other schemes are skipped.
func commentsURL(values []string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if strings.ContainsFunc(v, unicode.IsSpace) {
			continue
		}
		u, err := url.Parse(v)
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			return v
		}
	}
	return ""
}

// derivedTitleMax is the longest title, in characters, made from an item's
// text when it has no title of its own (Bluesky posts, microblog JSON Feeds).
const derivedTitleMax = 120

// firstLine is the first line of text that is not blank, cleaned.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if l := cleanText(line); l != "" {
			return l
		}
	}
	return ""
}

// truncateTitle shortens s to at most max characters, ending in an ellipsis
// when it cut something.
func truncateTitle(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)[:max-1]
	return strings.TrimSpace(string(r)) + "…"
}

// parseAtom parses an Atom 1.0 XML feed into normalized entries.
func parseAtom(body []byte) ([]parsedEntry, error) {
	var feed atomFeed
	if err := decodeXML(body, &feed); err != nil {
		return nil, fmt.Errorf("atom parse: %w", err)
	}

	var entries []parsedEntry
	for _, e := range feed.Entries {
		entry := parsedEntry{
			Title: cleanText(e.Title),
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
			entry.Author = cleanText(e.Author.Name)
		}

		// Date: prefer published, fallback to updated.
		dateStr := e.Published
		if dateStr == "" {
			dateStr = e.Updated
		}
		entry.Published, entry.BadDate = entryDate(dateStr)

		entries = append(entries, entry)
	}

	return entries, nil
}

// ── Date parsing ────────────────────────────────────────────────

// Feeds write dates in many shapes, whatever their format says: RSS is
// meant to use RFC 822 but real feeds drop the weekday or the seconds, use
// two-digit years or send an ISO date, and Atom feeds sometimes send RFC 822.
// Both parsers therefore accept every layout below. A date that still fails
// leaves the item without one: it sorts last, and the date window uses its
// fetch time.
//
// Examples from feeds this parser has met:
//
//	Fri, 02 Oct 26 12:00:00 +0000   CISA (two-digit year)
//	26 Sep 2026 11:58 +0000         Bluesky profile RSS (no weekday, no seconds)
//	2026-10-02 23:18:42.0           Cisco PSIRT (no zone)
var dateLayouts = func() []string {
	var layouts []string
	// RFC 822 / RFC 2822 and their common variants. "2" also reads a
	// two-digit day.
	for _, weekday := range []string{"Mon, ", ""} {
		for _, year := range []string{"2006", "06"} {
			for _, clock := range []string{"15:04:05", "15:04"} {
				for _, zone := range []string{"-0700", "-07:00", "MST"} {
					layouts = append(layouts, weekday+"2 Jan "+year+" "+clock+" "+zone)
				}
			}
		}
	}
	// ISO 8601 / RFC 3339. time.Parse accepts a fractional second after the
	// seconds even where a layout has none. A date without a zone is read as
	// UTC: Cisco's feed has none, and its newest item, captured at 00:45 UTC,
	// was 23:18 the evening before, which rules out US zones.
	return append(layouts,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
	)
}()

// rfc822Zones are the zone names RFC 822 defines. time.Parse knows a zone
// abbreviation only if the server's local zone uses it, and otherwise reads it
// as UTC, so "EDT" would be four hours off on a server in UTC. Other
// abbreviations are still read that way, which is closer than no date.
var rfc822Zones = map[string]string{
	"UT": "+0000", "UTC": "+0000", "GMT": "+0000", "Z": "+0000",
	"EST": "-0500", "EDT": "-0400",
	"CST": "-0600", "CDT": "-0500",
	"MST": "-0700", "MDT": "-0600",
	"PST": "-0800", "PDT": "-0700",
}

// normalizeTime converts a parsed feed date to UTC and drops sub-second
// precision. The database stores times as text and sorts them as text, so
// every stored value must have the same shape: the driver writes a time in
// its own UTC offset and with however many fractional digits it has, which
// would make "10:00:00+02:00" sort above "09:00:00+00:00" and
// "09:00:00.5+00:00" below "09:00:00+00:00".
func normalizeTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Second)
}

// parseDate parses an RSS or Atom item date into UTC with whole seconds.
func parseDate(s string) (time.Time, error) {
	fields := strings.Fields(s)
	if n := len(fields); n > 1 {
		if off, ok := rfc822Zones[strings.ToUpper(fields[n-1])]; ok {
			fields[n-1] = off
		}
	}
	norm := strings.Join(fields, " ")
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, norm); err == nil {
			return normalizeTime(t), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date %q", strings.TrimSpace(s))
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
	return cleanText(s)
}

// cleanText collapses runs of whitespace into single spaces and drops all
// other control characters. Feed text is untrusted and is eventually written
// to a terminal: XML rejects a raw ESC, but C1 controls (e.g. U+009B, the
// 8-bit CSI) pass through, and HTML-unescaping a double-encoded "&amp;#27;"
// yields a real ESC. Stripping them keeps feeds from injecting terminal
// escape sequences.
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
