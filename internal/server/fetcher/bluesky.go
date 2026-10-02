package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// TypeBluesky is the source type for a Bluesky account's own posts. The
// source URL is https://bsky.app/profile/<did>.
const TypeBluesky = "bluesky"

// blueskyAPIBase is the public AppView that serves getAuthorFeed and
// getProfile without authentication. A variable so tests can point it at an
// httptest server.
var blueskyAPIBase = "https://public.api.bsky.app"

const (
	blueskyProfileURLPrefix = "https://bsky.app/profile/"
	blueskyFeedLimit        = "50"
	// blueskyTitleMax is the longest post title, in characters.
	blueskyTitleMax = 120
	// maxXRPCBody caps what is read from an error or profile response.
	maxXRPCBody = 1 << 20
	// profileTimeout bounds a single handle resolution.
	profileTimeout = 10 * time.Second
)

// ── Actor syntax ────────────────────────────────────────────────

// Syntax from https://atproto.com/specs/handle and /specs/did.
var (
	blueskyHandleRE = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
	blueskyDIDRE    = regexp.MustCompile(`^did:[a-z]+:[a-zA-Z0-9._:%-]*[a-zA-Z0-9._-]$`)
)

// ParseBlueskyActor extracts the account (a DID or a handle) from what a
// user typed or what a source URL holds: "alice.bsky.social",
// "@alice.bsky.social", a bare DID, or https://bsky.app/profile/<actor>. A
// handle is returned lower-cased. Anything that is not valid atproto handle
// or DID syntax is an error, so the result is safe to put in a request.
func ParseBlueskyActor(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", errors.New("bluesky account is required")
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("invalid bluesky url: %v", err)
		}
		if u.Host != "bsky.app" && u.Host != "www.bsky.app" {
			return "", fmt.Errorf("bluesky url must be on bsky.app, got %q", u.Host)
		}
		rest, ok := strings.CutPrefix(strings.TrimSuffix(u.Path, "/"), "/profile/")
		if !ok || rest == "" || strings.Contains(rest, "/") {
			return "", errors.New("bluesky url must look like https://bsky.app/profile/<handle or did>")
		}
		s = rest
	}
	s = strings.TrimPrefix(s, "@")

	switch {
	case strings.HasPrefix(s, "did:"):
		if len(s) > 2048 || !blueskyDIDRE.MatchString(s) {
			return "", fmt.Errorf("invalid DID %q", s)
		}
		return s, nil
	case len(s) <= 253 && blueskyHandleRE.MatchString(s):
		return strings.ToLower(s), nil
	default:
		return "", fmt.Errorf("invalid bluesky handle or DID %q", s)
	}
}

// BlueskyProfileURL is the canonical source URL for an account.
func BlueskyProfileURL(did string) string {
	return blueskyProfileURLPrefix + did
}

// ── XRPC plumbing ───────────────────────────────────────────────

// XRPCError is an error response from the Bluesky API.
type XRPCError struct {
	Status  int
	Code    string // e.g. "InvalidRequest"
	Message string // e.g. "Profile not found"
}

func (e *XRPCError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return e.Code + ": " + e.Message
	case e.Code != "":
		return e.Code
	default:
		return fmt.Sprintf("HTTP %d", e.Status)
	}
}

// xrpcError reads the {"error","message"} body of a non-200 response. A
// body that is not that JSON leaves just the HTTP status.
func xrpcError(resp *http.Response) *XRPCError {
	e := &XRPCError{Status: resp.StatusCode}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxXRPCBody))
	if err != nil {
		return e
	}
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil {
		// The text is untrusted and ends up in the TUI status line.
		e.Code = cleanText(payload.Error)
		e.Message = cleanText(payload.Message)
	}
	return e
}

// blueskyFeedURL builds the getAuthorFeed request URL for a source URL.
func blueskyFeedURL(sourceURL string) (string, error) {
	actor, err := ParseBlueskyActor(sourceURL)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("actor", actor)
	q.Set("filter", "posts_no_replies")
	q.Set("limit", blueskyFeedLimit)
	return blueskyAPIBase + "/xrpc/app.bsky.feed.getAuthorFeed?" + q.Encode(), nil
}

// ── Profile resolution ──────────────────────────────────────────

// BlueskyProfile is the part of app.bsky.actor.defs#profileViewDetailed
// that adding a source needs.
type BlueskyProfile struct {
	DID         string
	Handle      string
	DisplayName string
}

// BlueskyResolver looks accounts up through the public API.
type BlueskyResolver struct {
	client doer
}

// NewBlueskyResolver returns a resolver that sends its requests through
// client, so the daemon's address restrictions apply to it.
func NewBlueskyResolver(client doer) *BlueskyResolver {
	return &BlueskyResolver{client: client}
}

// ResolveProfile calls app.bsky.actor.getProfile for actor (a handle or a
// DID, already syntax-checked). An error response from Bluesky is an
// *XRPCError; anything else is a network or decoding failure.
func (r *BlueskyResolver) ResolveProfile(ctx context.Context, actor string) (*BlueskyProfile, error) {
	ctx, cancel := context.WithTimeout(ctx, profileTimeout)
	defer cancel()

	q := url.Values{}
	q.Set("actor", actor)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		blueskyAPIBase+"/xrpc/app.bsky.actor.getProfile?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, xrpcError(resp)
	}

	var payload struct {
		DID         string `json:"did"`
		Handle      string `json:"handle"`
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxXRPCBody)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode profile: %w", err)
	}
	if !strings.HasPrefix(payload.DID, "did:") || len(payload.DID) > 2048 || !blueskyDIDRE.MatchString(payload.DID) {
		return nil, fmt.Errorf("profile has no valid DID")
	}
	return &BlueskyProfile{
		DID:         payload.DID,
		Handle:      cleanText(payload.Handle),
		DisplayName: cleanText(payload.DisplayName),
	}, nil
}

// ── Feed parsing ────────────────────────────────────────────────

const (
	reasonRepost     = "app.bsky.feed.defs#reasonRepost"
	embedImages      = "app.bsky.embed.images#view"
	embedExternal    = "app.bsky.embed.external#view"
	embedRecord      = "app.bsky.embed.record#view"
	embedRecordMedia = "app.bsky.embed.recordWithMedia#view"
	viewRecord       = "app.bsky.embed.record#viewRecord"
)

// The subset of app.bsky.feed.getAuthorFeed's output the fetcher reads.
type bskyFeed struct {
	Feed []bskyFeedViewPost `json:"feed"`
}

type bskyFeedViewPost struct {
	Post   bskyPost `json:"post"`
	Reason *struct {
		Type string `json:"$type"`
	} `json:"reason"`
}

type bskyPost struct {
	URI       string     `json:"uri"`
	Author    bskyAuthor `json:"author"`
	Record    bskyRecord `json:"record"`
	Embed     *bskyEmbed `json:"embed"`
	IndexedAt string     `json:"indexedAt"`
}

type bskyAuthor struct {
	DID         string `json:"did"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

type bskyRecord struct {
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

// bskyEmbed is the embed view union, flattened: which fields are set
// depends on Type.
type bskyEmbed struct {
	Type   string `json:"$type"`
	Images []struct {
		Alt string `json:"alt"`
	} `json:"images"`
	External *struct {
		URI   string `json:"uri"`
		Title string `json:"title"`
	} `json:"external"`
	// Record is the quoted post for record#view (a viewRecord, or a
	// not-found/blocked stub) and the record#view wrapper for
	// recordWithMedia#view, whose own Record holds the quoted post.
	Record *bskyRecordView `json:"record"`
	Media  *bskyEmbed      `json:"media"`
}

type bskyRecordView struct {
	Type   string     `json:"$type"`
	Author bskyAuthor `json:"author"`
	Value  struct {
		Text string `json:"text"`
	} `json:"value"`
	Record *bskyRecordView `json:"record"`
}

// parseBluesky turns a getAuthorFeed response into entries. Reposts are
// skipped: the feed is the account's own posts.
func parseBluesky(body []byte) ([]parsedEntry, error) {
	var feed bskyFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("bluesky parse: %w", err)
	}

	var entries []parsedEntry
	for _, fp := range feed.Feed {
		if fp.Reason != nil && fp.Reason.Type == reasonRepost {
			continue
		}
		p := fp.Post
		rkey := p.URI[strings.LastIndex(p.URI, "/")+1:]
		if p.URI == "" || rkey == "" {
			continue
		}

		parts := embedParts(p.Embed)

		entry := parsedEntry{
			Title:       blueskyTitle(p.Record.Text, parts),
			Description: blueskyDescription(p.Record.Text, parts),
			Author:      blueskyAuthorName(p.Author),
			GUID:        p.URI,
			Published:   blueskyPublished(p.Record.CreatedAt, p.IndexedAt),
		}
		if p.Author.DID != "" {
			entry.Link = blueskyProfileURLPrefix + p.Author.DID + "/post/" + rkey
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func blueskyAuthorName(a bskyAuthor) string {
	handle := cleanText(a.Handle)
	if handle == "" {
		return cleanText(a.DisplayName)
	}
	if name := cleanText(a.DisplayName); name != "" {
		return name + " (@" + handle + ")"
	}
	return "@" + handle
}

// embedText is the readable content of a post's embed.
type embedText struct {
	quote  string   // "↪ @handle: text"
	alts   []string // image alt texts
	card   string   // link card title and URL
	cardTi string   // link card title alone
}

// embedParts collects what an embed contributes. Embed types it does not
// know (video, unavailable records, ...) contribute nothing.
func embedParts(e *bskyEmbed) embedText {
	var out embedText
	collectEmbed(e, &out)
	return out
}

func collectEmbed(e *bskyEmbed, out *embedText) {
	if e == nil {
		return
	}
	switch e.Type {
	case embedImages:
		for _, img := range e.Images {
			if alt := cleanText(img.Alt); alt != "" {
				out.alts = append(out.alts, alt)
			}
		}
	case embedExternal:
		if e.External != nil {
			out.cardTi = cleanText(e.External.Title)
			out.card = strings.TrimSpace(strings.Join(
				[]string{out.cardTi, cleanText(e.External.URI)}, " "))
		}
	case embedRecord:
		out.quote = quoteText(e.Record)
	case embedRecordMedia:
		if e.Record != nil {
			out.quote = quoteText(e.Record.Record)
		}
		collectEmbed(e.Media, out)
	}
}

func quoteText(r *bskyRecordView) string {
	if r == nil || r.Type != viewRecord {
		return ""
	}
	handle := cleanText(r.Author.Handle)
	text := cleanText(r.Value.Text)
	if handle == "" || text == "" {
		return ""
	}
	return "↪ @" + handle + ": " + text
}

// blueskyDescription is the post text followed by its quoted post, image
// alt texts and link card, as one plain-text line like other feeds'.
func blueskyDescription(text string, e embedText) string {
	var parts []string
	if t := cleanText(text); t != "" {
		parts = append(parts, t)
	}
	if e.quote != "" {
		parts = append(parts, e.quote)
	}
	for _, alt := range e.alts {
		parts = append(parts, "[image: "+alt+"]")
	}
	if e.card != "" {
		parts = append(parts, e.card)
	}
	return strings.Join(parts, " ")
}

// blueskyTitle is the first line of the post, at most blueskyTitleMax
// characters. A post without text is titled by its first image's alt text or
// its link card.
func blueskyTitle(text string, e embedText) string {
	var title string
	for _, line := range strings.Split(text, "\n") {
		if title = cleanText(line); title != "" {
			break
		}
	}
	if title == "" {
		switch {
		case len(e.alts) > 0:
			title = e.alts[0]
		case e.cardTi != "":
			title = e.cardTi
		default:
			return "(no text)"
		}
	}
	if utf8.RuneCountInString(title) > blueskyTitleMax {
		r := []rune(title)[:blueskyTitleMax-1]
		title = strings.TrimSpace(string(r)) + "…"
	}
	return title
}

// blueskyPublished is the post's own createdAt, which the author's client
// sets and can get wrong, so a time after indexedAt (when Bluesky first saw
// the post) is replaced by indexedAt.
func blueskyPublished(createdAt, indexedAt string) *time.Time {
	created, cerr := time.Parse(time.RFC3339, strings.TrimSpace(createdAt))
	indexed, ierr := time.Parse(time.RFC3339, strings.TrimSpace(indexedAt))
	switch {
	case cerr != nil && ierr != nil:
		return nil
	case cerr != nil || (ierr == nil && created.After(indexed)):
		created = indexed
	}
	t := normalizeTime(created)
	return &t
}
