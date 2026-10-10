package fetcher

import (
	"strings"
	"testing"
	"time"
)

func TestParseJSONFeed(t *testing.T) {
	long := strings.Repeat("é", derivedTitleMax+10)
	body := `{
		"version": "https://jsonfeed.org/version/1.1",
		"title": "Example",
		"items": [
			{"id": "a", "url": "https://example.com/a", "title": "Titled", "summary": "Short", "content_text": "Longer text", "authors": [{"name": "Ann"}, {"name": ""}, {"name": "Ben"}], "date_published": "2026-10-01T10:00:00+02:00"},
			{"id": 42, "external_url": "https://other.example/x", "content_text": "\n\nFirst line here\nsecond line", "date_modified": "2026-10-02T00:00:00Z"},
			{"id": "c", "url": "https://example.com/c", "content_html": "<p>Some <b>html</b> &amp; more</p>"},
			{"id": "d", "url": "https://example.com/d", "content_text": "` + long + `"},
			{"id": "e", "url": "https://example.com/e", "title": "Bad date", "date_published": "yesterday"},
			{"url": "https://example.com/f", "title": "No id"}
		]
	}`
	entries, err := parseJSONFeed([]byte(body))
	if err != nil {
		t.Fatalf("parseJSONFeed: %v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("got %d entries, want 6", len(entries))
	}

	a := entries[0]
	if a.GUID != "a" || a.Link != "https://example.com/a" || a.Title != "Titled" {
		t.Errorf("a = %+v", a)
	}
	if a.Description != "Short" {
		t.Errorf("a.Description = %q, want the summary", a.Description)
	}
	if a.Author != "Ann, Ben" {
		t.Errorf("a.Author = %q", a.Author)
	}
	if want := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC); a.Published == nil || !a.Published.Equal(want) {
		t.Errorf("a.Published = %v, want %v", a.Published, want)
	}

	b := entries[1]
	if b.GUID != "42" {
		t.Errorf("b.GUID = %q, want the numeric id as text", b.GUID)
	}
	if b.Link != "https://other.example/x" {
		t.Errorf("b.Link = %q, want external_url", b.Link)
	}
	if b.Title != "First line here" {
		t.Errorf("b.Title = %q, want the first line of the text", b.Title)
	}
	if b.Description != "First line here second line" {
		t.Errorf("b.Description = %q", b.Description)
	}
	if want := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC); b.Published == nil || !b.Published.Equal(want) {
		t.Errorf("b.Published = %v, want date_modified", b.Published)
	}

	c := entries[2]
	if c.Description != "Some html & more" || c.Title != "Some html & more" {
		t.Errorf("c = %+v, want the HTML stripped in description and title", c)
	}

	d := entries[3]
	if want := strings.Repeat("é", derivedTitleMax-1) + "…"; d.Title != want {
		t.Errorf("d.Title has %d characters, want %d ending in an ellipsis", len([]rune(d.Title)), derivedTitleMax)
	}

	e := entries[4]
	if e.Published != nil || e.BadDate != "yesterday" {
		t.Errorf("e date = %v %q, want none and the raw text as bad", e.Published, e.BadDate)
	}

	if entries[5].GUID != "" || entries[5].Link != "https://example.com/f" {
		t.Errorf("f = %+v, want no GUID (dedup falls back to the link)", entries[5])
	}
}

func TestParseJSONFeed_Version10Author(t *testing.T) {
	body := `{"version": "https://jsonfeed.org/version/1", "items": [{"id": "1", "url": "https://x/1", "title": "t", "author": {"name": "Old Style"}}]}`
	entries, err := parseJSONFeed([]byte(body))
	if err != nil {
		t.Fatalf("parseJSONFeed: %v", err)
	}
	if entries[0].Author != "Old Style" {
		t.Errorf("Author = %q, want the 1.0 author", entries[0].Author)
	}
}

func TestParseJSONFeed_HTTPVersion(t *testing.T) {
	if _, err := parseJSONFeed([]byte(`{"version": "http://jsonfeed.org/version/1", "items": []}`)); err != nil {
		t.Errorf("an http:// version URL should be accepted: %v", err)
	}
}

func TestParseJSONFeed_NotAFeed(t *testing.T) {
	for _, body := range []string{
		`{"error": "not found"}`,
		`{"version": "https://example.com/version/1", "items": []}`,
		`{"version": "https://jsonfeed.org/version/2", "items": []}`,
		`{"version": 1}`,
		`{"version": "https://jsonfeed.org/version/1.1", "items": "nope"}`,
		`{"version": `,
	} {
		if entries, err := parseFeed([]byte(body)); err == nil {
			t.Errorf("parseFeed(%s) = %d entries, want an error", body, len(entries))
		}
	}
}

func TestParseJSONFeed_StripsControlCharacters(t *testing.T) {
	body := `{"version": "https://jsonfeed.org/version/1.1", "items": [
		{"id": "1", "url": "http://x/1", "title": "a\u009b31m\u001b[2J", "content_html": "hi &#27;[2J", "authors": [{"name": "b\u009bc"}]}
	]}`
	entries, err := parseJSONFeed([]byte(body))
	if err != nil {
		t.Fatalf("parseJSONFeed: %v", err)
	}
	e := entries[0]
	if e.Title != "a31m[2J" || e.Description != "hi [2J" || e.Author != "bc" {
		t.Errorf("entry = %+v, want control characters stripped", e)
	}
}

func TestJSONFeedID(t *testing.T) {
	tests := map[string]string{
		`"abc"`:  "abc",
		`" x "`:  "x",
		`12`:     "12",
		`1.5e3`:  "1.5e3",
		`null`:   "",
		`true`:   "",
		`{}`:     "",
		``:       "",
		`[1, 2]`: "",
	}
	for raw, want := range tests {
		if got := jsonFeedID([]byte(raw)); got != want {
			t.Errorf("jsonFeedID(%s) = %q, want %q", raw, got, want)
		}
	}
}
