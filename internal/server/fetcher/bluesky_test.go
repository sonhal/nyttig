package fetcher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sonhal/nyttig/internal/server/db"
)

// doerFunc adapts a function to doer.
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const bskyDID = "did:plc:z72i7hdynmk6r22z27h6tvur"

// feedPost builds a feedViewPost JSON object for the author alice. extra is
// spliced in after the record, for an embed or a reason.
func feedPost(rkey, text, createdAt, extra string) string {
	return `{"post":{"uri":"at://` + bskyDID + `/app.bsky.feed.post/` + rkey + `","cid":"bafy",
		"author":{"did":"` + bskyDID + `","handle":"alice.bsky.social","displayName":"Alice"},
		"record":{"$type":"app.bsky.feed.post","text":` + jsonString(text) + `,"createdAt":"` + createdAt + `"},
		"indexedAt":"2026-09-01T12:00:05.000Z"` + extra + `}}`
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			b.WriteString(`\u00`)
			b.WriteString(string("0123456789abcdef"[r>>4]))
			b.WriteString(string("0123456789abcdef"[r&15]))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func feedBody(posts ...string) []byte {
	return []byte(`{"cursor":"x","feed":[` + strings.Join(posts, ",") + `]}`)
}

func TestParseBluesky_PlainPost(t *testing.T) {
	entries, err := parseBluesky(feedBody(feedPost("3kabc", "Hello world\nsecond line", "2026-09-01T12:00:00.123Z", "")))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.GUID != "at://"+bskyDID+"/app.bsky.feed.post/3kabc" {
		t.Errorf("GUID = %q", e.GUID)
	}
	if e.Link != "https://bsky.app/profile/"+bskyDID+"/post/3kabc" {
		t.Errorf("Link = %q", e.Link)
	}
	if e.Author != "Alice (@alice.bsky.social)" {
		t.Errorf("Author = %q", e.Author)
	}
	if e.Title != "Hello world" {
		t.Errorf("Title = %q", e.Title)
	}
	if e.Description != "Hello world second line" {
		t.Errorf("Description = %q", e.Description)
	}
	want := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if e.Published == nil || !e.Published.Equal(want) {
		t.Errorf("Published = %v, want %v", e.Published, want)
	}
}

func TestParseBluesky_AuthorWithoutDisplayName(t *testing.T) {
	body := `{"feed":[{"post":{"uri":"at://did:plc:x/app.bsky.feed.post/1",
		"author":{"did":"did:plc:x","handle":"bob.example.com"},
		"record":{"text":"hi","createdAt":"2026-09-01T12:00:00Z"},"indexedAt":"2026-09-01T12:00:00Z"}}]}`
	entries, err := parseBluesky([]byte(body))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	if entries[0].Author != "@bob.example.com" {
		t.Errorf("Author = %q", entries[0].Author)
	}
}

func TestParseBluesky_RepostSkipped(t *testing.T) {
	repost := `,"reason":{"$type":"app.bsky.feed.defs#reasonRepost","by":{"did":"did:plc:me","handle":"me.example.com"},"indexedAt":"2026-09-01T12:00:00Z"}`
	pin := `,"reason":{"$type":"app.bsky.feed.defs#reasonPin"}`
	posts := []string{
		strings.TrimSuffix(feedPost("1", "reposted", "2026-09-01T12:00:00Z", ""), "}") + repost + "}",
		strings.TrimSuffix(feedPost("2", "pinned own post", "2026-09-01T12:00:00Z", ""), "}") + pin + "}",
	}
	entries, err := parseBluesky(feedBody(posts...))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Title != "pinned own post" {
		t.Fatalf("entries = %+v, want only the pinned own post", entries)
	}
}

func TestParseBluesky_QuotePost(t *testing.T) {
	embed := `,"embed":{"$type":"app.bsky.embed.record#view","record":{"$type":"app.bsky.embed.record#viewRecord",
		"uri":"at://did:plc:bob/app.bsky.feed.post/9","cid":"c","author":{"did":"did:plc:bob","handle":"bob.example.com","displayName":"Bob"},
		"value":{"$type":"app.bsky.feed.post","text":"original\nthought","createdAt":"2026-08-01T00:00:00Z"}}}`
	entries, _ := parseBluesky(feedBody(feedPost("1", "so true", "2026-09-01T12:00:00Z", embed)))
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (quote posts are kept)", len(entries))
	}
	if want := "so true ↪ @bob.example.com: original thought"; entries[0].Description != want {
		t.Errorf("Description = %q, want %q", entries[0].Description, want)
	}
}

func TestParseBluesky_UnavailableQuoteIgnored(t *testing.T) {
	embed := `,"embed":{"$type":"app.bsky.embed.record#view","record":{"$type":"app.bsky.embed.record#viewNotFound","uri":"at://x/y/z","notFound":true}}`
	entries, _ := parseBluesky(feedBody(feedPost("1", "gone", "2026-09-01T12:00:00Z", embed)))
	if len(entries) != 1 || entries[0].Description != "gone" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestParseBluesky_Images(t *testing.T) {
	embed := `,"embed":{"$type":"app.bsky.embed.images#view","images":[
		{"thumb":"https://cdn/t1","fullsize":"https://cdn/f1","alt":"A red bicycle"},
		{"thumb":"https://cdn/t2","fullsize":"https://cdn/f2","alt":""},
		{"thumb":"https://cdn/t3","fullsize":"https://cdn/f3","alt":"A blue door"}]}`
	entries, _ := parseBluesky(feedBody(
		feedPost("1", "new photos", "2026-09-01T12:00:00Z", embed),
		feedPost("2", "", "2026-09-01T12:00:00Z", embed),
	))
	if want := "new photos [image: A red bicycle] [image: A blue door]"; entries[0].Description != want {
		t.Errorf("Description = %q, want %q", entries[0].Description, want)
	}
	if entries[1].Title != "A red bicycle" {
		t.Errorf("empty-text title = %q, want first alt text", entries[1].Title)
	}
}

func TestParseBluesky_ExternalCard(t *testing.T) {
	embed := `,"embed":{"$type":"app.bsky.embed.external#view","external":{"uri":"https://example.com/story","title":"Big Story","description":"d","thumb":"https://cdn/t"}}`
	entries, _ := parseBluesky(feedBody(
		feedPost("1", "read this", "2026-09-01T12:00:00Z", embed),
		feedPost("2", "", "2026-09-01T12:00:00Z", embed),
	))
	if want := "read this Big Story https://example.com/story"; entries[0].Description != want {
		t.Errorf("Description = %q, want %q", entries[0].Description, want)
	}
	if entries[1].Title != "Big Story" {
		t.Errorf("empty-text title = %q, want card title", entries[1].Title)
	}
}

func TestParseBluesky_RecordWithMedia(t *testing.T) {
	embed := `,"embed":{"$type":"app.bsky.embed.recordWithMedia#view",
		"record":{"$type":"app.bsky.embed.record#view","record":{"$type":"app.bsky.embed.record#viewRecord",
			"uri":"at://did:plc:bob/app.bsky.feed.post/9","author":{"did":"did:plc:bob","handle":"bob.example.com"},
			"value":{"text":"quoted"}}},
		"media":{"$type":"app.bsky.embed.images#view","images":[{"alt":"chart"}]}}`
	entries, _ := parseBluesky(feedBody(feedPost("1", "look", "2026-09-01T12:00:00Z", embed)))
	if want := "look ↪ @bob.example.com: quoted [image: chart]"; entries[0].Description != want {
		t.Errorf("Description = %q, want %q", entries[0].Description, want)
	}
}

func TestParseBluesky_UnknownEmbedAndNoText(t *testing.T) {
	embed := `,"embed":{"$type":"app.bsky.embed.somethingNew#view","payload":{"a":1}}`
	entries, err := parseBluesky(feedBody(
		feedPost("1", "text stays", "2026-09-01T12:00:00Z", embed),
		feedPost("2", "", "2026-09-01T12:00:00Z", embed),
	))
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	if entries[0].Description != "text stays" {
		t.Errorf("Description = %q", entries[0].Description)
	}
	if entries[1].Title != "(no text)" || entries[1].Description != "" {
		t.Errorf("empty post: title=%q description=%q", entries[1].Title, entries[1].Description)
	}
}

func TestParseBluesky_PublishedClampedToIndexedAt(t *testing.T) {
	entries, _ := parseBluesky(feedBody(
		feedPost("1", "from the future", "2099-01-01T00:00:00Z", ""),
		feedPost("2", "no date", "not a date", ""),
	))
	want := time.Date(2026, 9, 1, 12, 0, 5, 0, time.UTC)
	for i, e := range entries {
		if e.Published == nil || !e.Published.Equal(want) {
			t.Errorf("entry %d Published = %v, want %v", i, e.Published, want)
		}
	}
}

func TestParseBluesky_StripsControlCharacters(t *testing.T) {
	entries, _ := parseBluesky(feedBody(feedPost("1", "he\x1b[31mllo\u009b wor\x07ld", "2026-09-01T12:00:00Z", "")))
	e := entries[0]
	for name, s := range map[string]string{"title": e.Title, "description": e.Description} {
		if s != "he[31mllo world" {
			t.Errorf("%s = %q, want control characters removed", name, s)
		}
	}
}

func TestParseBluesky_TitleTruncation(t *testing.T) {
	long := strings.Repeat("é", 200)
	exact := strings.Repeat("x", blueskyTitleMax)
	entries, _ := parseBluesky(feedBody(
		feedPost("1", long+"\nnext", "2026-09-01T12:00:00Z", ""),
		feedPost("2", exact, "2026-09-01T12:00:00Z", ""),
		feedPost("3", "\n\n  first real line  \nmore", "2026-09-01T12:00:00Z", ""),
	))
	if want := strings.Repeat("é", blueskyTitleMax-1) + "…"; entries[0].Title != want {
		t.Errorf("long title = %q, want %d é plus an ellipsis", entries[0].Title, blueskyTitleMax-1)
	}
	if entries[1].Title != exact {
		t.Errorf("a title of exactly %d characters must not be truncated", blueskyTitleMax)
	}
	if entries[2].Title != "first real line" {
		t.Errorf("title = %q, want the first non-empty line", entries[2].Title)
	}
}

func TestParseBluesky_InvalidJSON(t *testing.T) {
	if _, err := parseBluesky([]byte("<html>")); err == nil {
		t.Error("expected an error for a non-JSON body")
	}
	if entries, err := parseBluesky([]byte(`{"feed":null}`)); err != nil || len(entries) != 0 {
		t.Errorf("null feed: entries=%v err=%v", entries, err)
	}
}

func TestParseBlueskyActor(t *testing.T) {
	ok := map[string]string{
		"alice.bsky.social":                           "alice.bsky.social",
		"@Alice.Bsky.Social":                          "alice.bsky.social",
		"  alice.bsky.social \n":                      "alice.bsky.social",
		"https://bsky.app/profile/alice.bsky.social":  "alice.bsky.social",
		"https://bsky.app/profile/alice.bsky.social/": "alice.bsky.social",
		"https://bsky.app/profile/" + bskyDID:         bskyDID,
		bskyDID:                                       bskyDID,
		"did:web:example.com":                         "did:web:example.com",
		"jay.example.co.uk":                           "jay.example.co.uk",
	}
	for in, want := range ok {
		got, err := ParseBlueskyActor(in)
		if err != nil || got != want {
			t.Errorf("ParseBlueskyActor(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "   ", "alice", "alice..social", "-alice.bsky.social", "alice.bsky.social-",
		"alice.bsky.social/extra", "alice bsky.social", "alice.bsky.social&limit=1",
		"alice.bsky.1", "did:", "did:plc:", "did:PLC:abc", "did:plc:ab/c", "did:plc:abc:",
		"https://example.com/profile/alice.bsky.social",
		"https://bsky.app/profile/alice.bsky.social/post/3k",
		"https://bsky.app/feed/alice.bsky.social",
		"https://bsky.app/profile/",
		"ftp://bsky.app/profile/alice.bsky.social",
		strings.Repeat("a", 64) + ".example.com",
	}
	for _, in := range bad {
		if got, err := ParseBlueskyActor(in); err == nil {
			t.Errorf("ParseBlueskyActor(%q) = %q, want an error", in, got)
		}
	}
}

func TestFetch_BlueskyRequestAndInsert(t *testing.T) {
	database := setupDB(t)
	defer func() { _ = database.Close() }()

	var got *http.Request
	client := doerFunc(func(req *http.Request) (*http.Response, error) {
		got = req
		return jsonResponse(200, string(feedBody(
			feedPost("1", "first", "2026-09-01T12:00:00Z", ""),
			feedPost("2", "second", "2026-09-01T12:01:00Z", ""),
		))), nil
	})

	src := &db.Source{Name: "Alice", URL: "https://bsky.app/profile/" + bskyDID, Type: "bluesky", RefreshSec: 3600, Enabled: true}
	id, err := db.InsertSource(database, src)
	if err != nil {
		t.Fatal(err)
	}
	src.ID = id

	result, err := FetchWithClient(database, src, client)
	if err != nil || result.FetchError != "" {
		t.Fatalf("err=%v fetchError=%q", err, result.FetchError)
	}
	if len(result.NewItems) != 2 || countItems(t, database) != 2 {
		t.Errorf("new items = %d, stored = %d, want 2", len(result.NewItems), countItems(t, database))
	}

	if got.Method != http.MethodGet || got.URL.Scheme != "https" || got.URL.Host != "public.api.bsky.app" ||
		got.URL.Path != "/xrpc/app.bsky.feed.getAuthorFeed" {
		t.Errorf("request = %s %s", got.Method, got.URL)
	}
	wantQuery := url.Values{"actor": {bskyDID}, "filter": {"posts_no_replies"}, "limit": {"50"}}
	if q := got.URL.Query(); q.Encode() != wantQuery.Encode() {
		t.Errorf("query = %v, want %v", q, wantQuery)
	}
	if got.Header.Get("User-Agent") != userAgent {
		t.Errorf("User-Agent = %q", got.Header.Get("User-Agent"))
	}

	// A second fetch of the same posts adds nothing.
	result, _ = FetchWithClient(database, src, client)
	if len(result.NewItems) != 0 || countItems(t, database) != 2 {
		t.Errorf("refetch: new items = %d, stored = %d", len(result.NewItems), countItems(t, database))
	}
}

func TestFetch_BlueskyHandleURL(t *testing.T) {
	database := setupDB(t)
	defer func() { _ = database.Close() }()

	var got *http.Request
	client := doerFunc(func(req *http.Request) (*http.Response, error) {
		got = req
		return jsonResponse(200, `{"feed":[]}`), nil
	})
	src := &db.Source{Name: "Alice", URL: "https://bsky.app/profile/Alice.bsky.social", Type: "bluesky", RefreshSec: 3600, Enabled: true}
	src.ID, _ = db.InsertSource(database, src)

	if _, err := FetchWithClient(database, src, client); err != nil {
		t.Fatal(err)
	}
	if a := got.URL.Query().Get("actor"); a != "alice.bsky.social" {
		t.Errorf("actor = %q", a)
	}
}

func TestFetch_BlueskyBadActorIsFetchError(t *testing.T) {
	database := setupDB(t)
	defer func() { _ = database.Close() }()

	called := false
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return jsonResponse(200, `{"feed":[]}`), nil
	})
	src := &db.Source{Name: "Bad", URL: "https://evil.example/profile/alice.bsky.social&x=1", Type: "bluesky", RefreshSec: 3600, Enabled: true}
	src.ID, _ = db.InsertSource(database, src)

	result, err := FetchWithClient(database, src, client)
	if err != nil {
		t.Fatal(err)
	}
	if called || result.FetchError == "" {
		t.Errorf("called=%v fetchError=%q, want no request and a fetch error", called, result.FetchError)
	}
	stored, _ := db.GetSource(database, src.ID)
	if stored.FetchError == nil || *stored.FetchError == "" {
		t.Error("expected fetch_error to be stored on the source")
	}
}

func TestFetch_BlueskyXRPCError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"profile not found", 400, `{"error":"InvalidRequest","message":"Profile not found"}`, "InvalidRequest: Profile not found"},
		{"rate limited", 429, `{"error":"RateLimitExceeded","message":"Rate Limit Exceeded"}`, "RateLimitExceeded: Rate Limit Exceeded"},
		{"error without message", 500, `{"error":"InternalServerError"}`, "InternalServerError"},
		{"not json", 502, `<html>bad gateway</html>`, "HTTP 502"},
		{"empty", 503, ``, "HTTP 503"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			database := setupDB(t)
			defer func() { _ = database.Close() }()
			client := doerFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(tt.status, tt.body), nil })
			src := &db.Source{Name: "A", URL: "https://bsky.app/profile/" + bskyDID, Type: "bluesky", RefreshSec: 3600, Enabled: true}
			src.ID, _ = db.InsertSource(database, src)

			result, err := FetchWithClient(database, src, client)
			if err != nil {
				t.Fatal(err)
			}
			if result.FetchError != tt.want {
				t.Errorf("FetchError = %q, want %q", result.FetchError, tt.want)
			}
			stored, _ := db.GetSource(database, src.ID)
			if stored.FetchError == nil || *stored.FetchError != tt.want {
				t.Errorf("stored fetch_error = %v, want %q", stored.FetchError, tt.want)
			}
		})
	}
}

func TestFetch_BlueskyInvalidBody(t *testing.T) {
	database := setupDB(t)
	defer func() { _ = database.Close() }()
	client := doerFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(200, "<rss/>"), nil })
	src := &db.Source{Name: "A", URL: "https://bsky.app/profile/" + bskyDID, Type: "bluesky", RefreshSec: 3600, Enabled: true}
	src.ID, _ = db.InsertSource(database, src)

	result, _ := FetchWithClient(database, src, client)
	if !strings.HasPrefix(result.FetchError, "bluesky parse:") {
		t.Errorf("FetchError = %q", result.FetchError)
	}
}

func TestBlueskyResolver(t *testing.T) {
	var got *http.Request
	ok := doerFunc(func(req *http.Request) (*http.Response, error) {
		got = req
		return jsonResponse(200, `{"did":"`+bskyDID+`","handle":"alice.bsky.social","displayName":"Alice\u0007 A.","followersCount":3}`), nil
	})
	p, err := NewBlueskyResolver(ok).ResolveProfile(context.Background(), "alice.bsky.social")
	if err != nil {
		t.Fatal(err)
	}
	if p.DID != bskyDID || p.Handle != "alice.bsky.social" || p.DisplayName != "Alice A." {
		t.Errorf("profile = %+v", p)
	}
	if got.URL.Path != "/xrpc/app.bsky.actor.getProfile" || got.URL.Query().Get("actor") != "alice.bsky.social" {
		t.Errorf("request = %s", got.URL)
	}

	notFound := doerFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(400, `{"error":"InvalidRequest","message":"Profile not found"}`), nil
	})
	_, err = NewBlueskyResolver(notFound).ResolveProfile(context.Background(), "nobody.bsky.social")
	var xerr *XRPCError
	if !errors.As(err, &xerr) || xerr.Status != 400 || err.Error() != "InvalidRequest: Profile not found" {
		t.Errorf("err = %v, want an XRPCError", err)
	}

	netErr := doerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") })
	_, err = NewBlueskyResolver(netErr).ResolveProfile(context.Background(), "alice.bsky.social")
	if err == nil || errors.As(err, &xerr) {
		t.Errorf("err = %v, want a plain network error", err)
	}

	noDID := doerFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"handle":"x.example.com"}`), nil
	})
	if _, err := NewBlueskyResolver(noDID).ResolveProfile(context.Background(), "x.example.com"); err == nil {
		t.Error("expected an error for a profile without a DID")
	}
}
