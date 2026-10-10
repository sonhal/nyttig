package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sonhal/nyttig/internal/server/db"
	"github.com/sonhal/nyttig/internal/server/fetcher"
	"github.com/sonhal/nyttig/internal/server/service"
	"github.com/sonhal/nyttig/internal/server/tagger"
)

// recordingTransport answers every request with body and records the URLs
// it was asked for.
type recordingTransport struct {
	body string

	mu   sync.Mutex
	urls []string
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.urls = append(rt.urls, req.URL.String())
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(rt.body)),
		Request:    req,
	}, nil
}

const blueskyAuthorFeed = `{"feed":[{"post":{
	"uri":"at://did:plc:abc123/app.bsky.feed.post/3kpost",
	"cid":"bafy",
	"author":{"did":"did:plc:abc123","handle":"alice.bsky.social","displayName":"Alice"},
	"record":{"$type":"app.bsky.feed.post","text":"Hello from Bluesky","createdAt":"2026-10-01T12:00:00Z"},
	"indexedAt":"2026-10-01T12:00:01Z"}}]}`

// TestDoFetch_BlueskyUsesAPI runs a Bluesky source through the daemon's own
// path (store -> scheduler.Source -> doFetch). The source's type has to
// survive the scheduler round trip: without it the fetcher requested the
// bsky.app profile page and failed with "unrecognized feed format".
func TestDoFetch_BlueskyUsesAPI(t *testing.T) {
	database := openSeedDB(t)
	id, err := db.InsertSource(database, &db.Source{
		Name: "Alice", URL: "https://bsky.app/profile/did:plc:abc123",
		Type: "bluesky", RefreshSec: 900, Enabled: true,
	})
	if err != nil {
		t.Fatalf("InsertSource: %v", err)
	}

	src, err := (&dbSourceStore{db: database}).GetSource(id)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}

	rt := &recordingTransport{body: blueskyAuthorFeed}
	logger := slog.New(slog.DiscardHandler)
	tgr := tagger.New(&dbTaggerStore{db: database}, logger)
	hub := service.NewHub()
	defer hub.Close()

	if err := doFetch(context.Background(), database, &http.Client{Transport: rt}, src, tgr, hub, logger); err != nil {
		t.Fatalf("doFetch: %v", err)
	}

	if len(rt.urls) != 1 || !strings.HasPrefix(rt.urls[0], "https://public.api.bsky.app/xrpc/app.bsky.feed.getAuthorFeed?") {
		t.Fatalf("requested %v, want one getAuthorFeed call", rt.urls)
	}
	got, err := db.GetSource(database, id)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if got.FetchError != nil && *got.FetchError != "" {
		t.Fatalf("fetch_error = %q", *got.FetchError)
	}
	items, _, err := db.ListItems(database, db.ItemFilter{SourceID: id})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Hello from Bluesky" {
		t.Fatalf("items = %+v, want the one post", items)
	}
}

// TestDoFetch_EUVD runs an EUVD source through the daemon's path: the search
// is paged, the record becomes an item, and its scores become assessments;
// a refetch with a new score updates the assessment in place.
func TestDoFetch_EUVD(t *testing.T) {
	database := openSeedDB(t)
	id, err := db.InsertSource(database, &db.Source{
		Name: "EUVD", URL: fetcher.EUVDDefaultURL, Type: "euvd", RefreshSec: 3600, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	src, err := (&dbSourceStore{db: database}).GetSource(id)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	tgr := tagger.New(&dbTaggerStore{db: database}, logger)
	hub := service.NewHub()
	defer hub.Close()

	page := func(score float64) string {
		return `{"items":[{"id":"EUVD-2026-1","description":"Bug","aliases":"CVE-2026-0001\n",
			"datePublished":"Oct 1, 2026, 10:00:00 AM","baseScore":` + strconv.FormatFloat(score, 'f', -1, 64) + `}],"total":1}`
	}
	rt := &recordingTransport{body: page(7.5)}
	if err := doFetch(context.Background(), database, &http.Client{Transport: rt}, src, tgr, hub, logger); err != nil {
		t.Fatal(err)
	}
	if len(rt.urls) != 1 || !strings.HasPrefix(rt.urls[0], "https://euvdservices.enisa.europa.eu/api/search?") ||
		!strings.Contains(rt.urls[0], "exploited=true") {
		t.Fatalf("requested %v", rt.urls)
	}
	score := func() float64 {
		t.Helper()
		items, _, err := db.ListItems(database, db.ItemFilter{SourceID: id})
		if err != nil || len(items) != 1 || items[0].Title != "CVE-2026-0001: Bug" || len(items[0].Assessments) != 1 {
			t.Fatalf("items = %+v, %v", items, err)
		}
		return *items[0].Assessments[0].Score
	}
	if s := score(); s != 0.75 {
		t.Errorf("score = %v", s)
	}

	rt.body = page(9.1)
	if err := doFetch(context.Background(), database, &http.Client{Transport: rt}, src, tgr, hub, logger); err != nil {
		t.Fatal(err)
	}
	if s := score(); s != 0.91 {
		t.Errorf("score after refetch = %v", s)
	}
}
