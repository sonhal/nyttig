package fetcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sonhal/nyttig/internal/server/db"
)

// ── Format detection ────────────────────────────────────────────

func TestFeedKind(t *testing.T) {
	const bom = "\xef\xbb\xbf"
	rdf10 := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/"><channel/></rdf:RDF>`
	rdf090 := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://my.netscape.com/rdf/simple/0.9/"><channel/></rdf:RDF>`
	tests := []struct {
		name, body, want string
	}{
		{"rss", `<?xml version="1.0"?><rss version="2.0"><channel/></rss>`, kindRSS},
		{"atom", `<feed xmlns="http://www.w3.org/2005/Atom"></feed>`, kindAtom},
		{"rdf 1.0", rdf10, kindRDF},
		{"rdf 0.90", rdf090, kindRDF},
		{"rdf other prefix", `<r:RDF xmlns:r="http://www.w3.org/1999/02/22-rdf-syntax-ns#"/>`, kindRDF},
		{"json", `{"version":"https://jsonfeed.org/version/1.1"}`, kindJSON},
		{"json after whitespace", "\n\t {}", kindJSON},
		{"bom rss", bom + `<rss/>`, kindRSS},
		{"bom atom", bom + `<?xml version="1.0"?><feed/>`, kindAtom},
		{"bom rdf", bom + rdf10, kindRDF},
		{"bom json", bom + `{}`, kindJSON},
		{"comment and doctype before root", `<?xml version="1.0"?>
<!-- generated -->
<!DOCTYPE rss [<!ENTITY nbsp "&#160;">]>
<rss version="2.0"/>`, kindRSS},
		{"stylesheet before root", `<?xml version="1.0"?><?xml-stylesheet href="s.xsl"?><feed/>`, kindAtom},
		{"iso-8859-1 rdf", "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>" + rdf10, kindRDF},
		{"html", `<!DOCTYPE html><html><body>not a feed</body></html>`, ""},
		{"text", `this is not XML at all`, ""},
		{"empty", ``, ""},
		{"whitespace", "  \n", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := feedKind([]byte(tc.body)); got != tc.want {
				t.Errorf("feedKind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseFeed_BOM(t *testing.T) {
	body := []byte("\xef\xbb\xbf<?xml version=\"1.0\"?><rss version=\"2.0\"><channel><item><title>A</title><link>http://x/1</link></item></channel></rss>")
	entries, err := parseFeed(body)
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(entries) != 1 || entries[0].Title != "A" {
		t.Fatalf("entries = %+v, want one titled A", entries)
	}
}

func TestParseFeed_Unrecognized(t *testing.T) {
	for _, body := range []string{`<html><body/></html>`, `not a feed`, ``} {
		if _, err := parseFeed([]byte(body)); err == nil || err.Error() != "unrecognized feed format" {
			t.Errorf("parseFeed(%q) err = %v, want unrecognized feed format", body, err)
		}
	}
}

// ── RSS 1.0 (RDF) ───────────────────────────────────────────────

const rdfFixture = `<?xml version="1.0" encoding="UTF-8"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
         xmlns="http://purl.org/rss/1.0/"
         xmlns:dc="http://purl.org/dc/elements/1.1/"
         xmlns:slash="http://purl.org/rss/1.0/modules/slash/">
  <channel rdf:about="https://example.com/">
    <title>Example</title>
    <link>https://example.com/</link>
    <description>Channel description</description>
    <items><rdf:Seq>
      <rdf:li rdf:resource="https://example.com/a"/>
      <rdf:li rdf:resource="https://example.com/b"/>
    </rdf:Seq></items>
  </channel>
  <item rdf:about="https://example.com/a">
    <title>First &amp; foremost</title>
    <link>https://example.com/a</link>
    <description>&lt;p&gt;Some &lt;b&gt;bold&lt;/b&gt; text&lt;/p&gt;</description>
    <dc:creator>Alice</dc:creator>
    <dc:date>2026-10-02T14:30:00+02:00</dc:date>
    <slash:comments>42</slash:comments>
  </item>
  <item rdf:about="https://example.com/b">
    <title>Second</title>
    <link>https://example.com/b</link>
    <dc:creator>Bob</dc:creator>
    <dc:creator>Carol</dc:creator>
  </item>
</rdf:RDF>`

func TestParseRDF(t *testing.T) {
	entries, err := parseFeed([]byte(rdfFixture))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (items are outside <channel>)", len(entries))
	}
	a := entries[0]
	if a.Title != "First & foremost" || a.Link != "https://example.com/a" || a.GUID != "https://example.com/a" {
		t.Errorf("first entry = %+v", a)
	}
	if a.Description != "Some bold text" {
		t.Errorf("Description = %q, want the HTML stripped", a.Description)
	}
	if a.Author != "Alice" {
		t.Errorf("Author = %q, want Alice", a.Author)
	}
	if want := time.Date(2026, 10, 2, 12, 30, 0, 0, time.UTC); a.Published == nil || !a.Published.Equal(want) {
		t.Errorf("Published = %v, want %v", a.Published, want)
	}
	b := entries[1]
	if b.Author != "Bob, Carol" {
		t.Errorf("Author = %q, want both creators", b.Author)
	}
	if b.Published != nil || b.BadDate != "" {
		t.Errorf("an item without dc:date should have no date, got %v %q", b.Published, b.BadDate)
	}
}

func TestParseRDF_090(t *testing.T) {
	body := `<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://my.netscape.com/rdf/simple/0.9/">
  <channel><title>Old</title><link>https://old.example/</link></channel>
  <item><title>Story</title><link>https://old.example/story</link></item>
</rdf:RDF>`
	entries, err := parseFeed([]byte(body))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	if len(entries) != 1 || entries[0].Title != "Story" || entries[0].Link != "https://old.example/story" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].GUID != "" {
		t.Errorf("GUID = %q, want empty (dedup falls back to the link)", entries[0].GUID)
	}
}

func TestParseRDF_StripsControlCharacters(t *testing.T) {
	body := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/" xmlns:dc="http://purl.org/dc/elements/1.1/">
<item rdf:about="1"><title>a&#155;31m</title><link>http://x/1</link><description>hi &amp;#27;[2J</description><dc:creator>b&#155;c</dc:creator></item></rdf:RDF>`
	entries, err := parseFeed([]byte(body))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	e := entries[0]
	if e.Title != "a31m" || e.Description != "hi [2J" || e.Author != "bc" {
		t.Errorf("entry = %+v, want control characters stripped", e)
	}
}

// ── RSS 2.0: dc:creator and comments ────────────────────────────

func rssWithItem(item string) []byte {
	return []byte(`<?xml version="1.0"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:slash="http://purl.org/rss/1.0/modules/slash/">
<channel><title>T</title><item>` + item + `</item></channel></rss>`)
}

func TestParseRSS_DCCreator(t *testing.T) {
	tests := []struct {
		name, item, want string
	}{
		{"creator wins over author", `<author>a@example.com (Alice)</author><dc:creator>Alice Doe</dc:creator>`, "Alice Doe"},
		{"several creators", `<dc:creator>Ada</dc:creator><dc:creator> </dc:creator><dc:creator>Grace</dc:creator>`, "Ada, Grace"},
		{"comma list kept", `<dc:creator>A. Turing, K. Gödel</dc:creator>`, "A. Turing, K. Gödel"},
		{"author alone", `<author>bob@example.com</author>`, "bob@example.com"},
		{"blank creator falls back", `<author>bob@example.com</author><dc:creator>  </dc:creator>`, "bob@example.com"},
		{"neither", ``, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := parseFeed(rssWithItem(`<title>x</title><link>http://x/1</link>` + tc.item))
			if err != nil {
				t.Fatalf("parseFeed: %v", err)
			}
			if entries[0].Author != tc.want {
				t.Errorf("Author = %q, want %q", entries[0].Author, tc.want)
			}
		})
	}
}

func TestParseRSS_Comments(t *testing.T) {
	const thread = "https://news.ycombinator.com/item?id=1"
	tests := []struct {
		name, item, link, desc string
	}{
		{
			name: "appended to the description",
			item: `<link>https://example.com/a</link><description>An article</description><comments>` + thread + `</comments>`,
			link: "https://example.com/a", desc: "An article Comments: " + thread,
		},
		{
			name: "empty description",
			item: `<link>https://example.com/a</link><comments>` + thread + `</comments>`,
			link: "https://example.com/a", desc: "Comments: " + thread,
		},
		{
			// hnrss.org's descriptions already carry the URL.
			name: "already in the description",
			item: `<link>https://example.com/a</link><description>&lt;p&gt;Comments URL: &lt;a href="` + thread + `"&gt;` + thread + `&lt;/a&gt;&lt;/p&gt;</description><comments>` + thread + `</comments>`,
			link: "https://example.com/a", desc: "Comments URL: " + thread,
		},
		{
			// Ask HN: the link is the thread.
			name: "same as the link",
			item: `<link>` + thread + `</link><description>Ask</description><comments>` + thread + `</comments>`,
			link: thread, desc: "Ask",
		},
		{
			name: "no link: the thread becomes the link",
			item: `<description>Text post</description><comments>` + thread + `</comments>`,
			link: thread, desc: "Text post",
		},
		{
			// WordPress sends a slash:comments count next to the URL.
			name: "slash count ignored",
			item: `<link>https://example.com/a</link><slash:comments>3</slash:comments><comments>https://example.com/a#comments</comments>`,
			link: "https://example.com/a", desc: "Comments: https://example.com/a#comments",
		},
		{
			name: "slash count alone",
			item: `<link>https://example.com/a</link><description>d</description><slash:comments>3</slash:comments>`,
			link: "https://example.com/a", desc: "d",
		},
		{
			name: "javascript scheme ignored",
			item: `<link>https://example.com/a</link><description>d</description><comments>javascript:alert(1)</comments>`,
			link: "https://example.com/a", desc: "d",
		},
		{
			name: "relative URL ignored",
			item: `<link>https://example.com/a</link><description>d</description><comments>/item?id=1</comments>`,
			link: "https://example.com/a", desc: "d",
		},
		{
			name: "whitespace trimmed, inner space rejected",
			item: `<link>https://example.com/a</link><description>d</description><comments>  https://x.example/a b </comments>`,
			link: "https://example.com/a", desc: "d",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := parseFeed(rssWithItem(`<title>x</title>` + tc.item))
			if err != nil {
				t.Fatalf("parseFeed: %v", err)
			}
			e := entries[0]
			if e.Link != tc.link || e.Description != tc.desc {
				t.Errorf("link, description = %q, %q; want %q, %q", e.Link, e.Description, tc.link, tc.desc)
			}
		})
	}
}

// ── End to end ──────────────────────────────────────────────────

// fetchBody serves body with contentType and fetches it as an rss source.
func fetchBody(t *testing.T, contentType, body string) []*db.Item {
	t.Helper()
	database := setupDB(t)
	t.Cleanup(func() { _ = database.Close() })

	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	src := insertTestSource(t, database, "Feed", srv.URL)
	result, err := FetchWithClient(database, src, srv.Client())
	if err != nil {
		t.Fatalf("FetchWithClient: %v", err)
	}
	if result.FetchError != "" {
		t.Fatalf("fetch error: %s", result.FetchError)
	}
	for _, mt := range []string{"application/feed+json", "application/rdf+xml"} {
		if !strings.Contains(accept, mt) {
			t.Errorf("Accept = %q, want it to include %s", accept, mt)
		}
	}
	var items []*db.Item
	for _, it := range result.NewItems {
		got, err := db.GetItem(database, it.ID)
		if err != nil || got == nil {
			t.Fatalf("GetItem(%d): %v", it.ID, err)
		}
		items = append(items, got)
	}
	return items
}

func TestFetch_RDFFeed(t *testing.T) {
	items := fetchBody(t, "application/rdf+xml", rdfFixture)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Title != "First & foremost" || items[0].Author == nil || *items[0].Author != "Alice" {
		t.Errorf("first item = %+v", items[0])
	}
}

func TestFetch_JSONFeed(t *testing.T) {
	items := fetchBody(t, "application/feed+json", `{
		"version": "https://jsonfeed.org/version/1.1",
		"title": "Example",
		"items": [
			{"id": "1", "url": "https://example.com/1", "title": "One", "content_text": "Body", "date_published": "2026-10-01T08:00:00Z", "authors": [{"name": "Ann"}]}
		]
	}`)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.Title != "One" || it.Link != "https://example.com/1" || it.Author == nil || *it.Author != "Ann" {
		t.Errorf("item = %+v", it)
	}
	if it.Published == nil || !it.Published.Equal(time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("Published = %v", it.Published)
	}
}
