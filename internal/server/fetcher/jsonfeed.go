package fetcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ── JSON Feed 1.0 / 1.1 ─────────────────────────────────────────

// jsonFeedVersionPrefix starts the version URL of every JSON Feed 1.x
// (https://jsonfeed.org/version/1 and /version/1.1).
const jsonFeedVersionPrefix = "jsonfeed.org/version/1"

// The subset of JSON Feed (https://www.jsonfeed.org/version/1.1/) the
// fetcher reads.
type jsonFeed struct {
	Version string         `json:"version"`
	Items   []jsonFeedItem `json:"items"`
}

type jsonFeedItem struct {
	// ID is a string in the spec; some 1.0 feeds send a number.
	ID            json.RawMessage  `json:"id"`
	URL           string           `json:"url"`
	ExternalURL   string           `json:"external_url"`
	Title         string           `json:"title"`
	ContentText   string           `json:"content_text"`
	ContentHTML   string           `json:"content_html"`
	Summary       string           `json:"summary"`
	DatePublished string           `json:"date_published"`
	DateModified  string           `json:"date_modified"`
	Authors       []jsonFeedAuthor `json:"authors"` // 1.1
	Author        *jsonFeedAuthor  `json:"author"`  // 1.0
}

type jsonFeedAuthor struct {
	Name string `json:"name"`
}

// parseJSONFeed parses a JSON Feed 1.0 or 1.1 document. JSON without a 1.x
// version is an error, so a JSON error page shows as a fetch error rather
// than as an empty feed.
func parseJSONFeed(body []byte) ([]parsedEntry, error) {
	var feed jsonFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("json feed parse: %w", err)
	}
	v := strings.TrimSpace(feed.Version)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://")
	if !strings.HasPrefix(v, jsonFeedVersionPrefix) {
		return nil, fmt.Errorf("not a JSON Feed: missing or unknown version")
	}

	var entries []parsedEntry
	for _, item := range feed.Items {
		entry := parsedEntry{
			GUID: jsonFeedID(item.ID),
			Link: strings.TrimSpace(item.URL),
		}
		if entry.Link == "" {
			entry.Link = strings.TrimSpace(item.ExternalURL)
		}

		html := sanitizeHTML(item.ContentHTML)
		switch {
		case cleanText(item.Summary) != "":
			entry.Description = cleanText(item.Summary)
		case cleanText(item.ContentText) != "":
			entry.Description = cleanText(item.ContentText)
		default:
			entry.Description = html
		}

		// Microblog items have no title: use the start of the text.
		entry.Title = cleanText(item.Title)
		if entry.Title == "" {
			text := firstLine(item.ContentText)
			if text == "" {
				text = html
			}
			entry.Title = truncateTitle(text, derivedTitleMax)
		}

		var names []string
		for _, a := range item.Authors {
			names = append(names, a.Name)
		}
		if len(names) == 0 && item.Author != nil {
			names = append(names, item.Author.Name)
		}
		entry.Author = joinNames(names)

		date := item.DatePublished
		if strings.TrimSpace(date) == "" {
			date = item.DateModified
		}
		entry.Published, entry.BadDate = entryDate(date)

		entries = append(entries, entry)
	}
	return entries, nil
}

// jsonFeedID reads an item id that is a string or a number; anything else
// is no id, and dedup falls back to the link.
func jsonFeedID(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}
