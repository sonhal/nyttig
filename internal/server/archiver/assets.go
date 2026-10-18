package archiver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const userAgent = "Nyttig/0.1 (news-aggregator)"

type assetKind int

const (
	kindSheet assetKind = iota
	kindImage
	kindFont
)

type assetReq struct {
	url  string
	kind assetKind
}

// asset is the outcome of one fetch. Entries are written before the batch
// that created them returns, so readers need no lock.
type asset struct {
	body  []byte
	ctype string // media type without parameters
	ok    bool
}

// assets fetches subresources under the job's limits and counts the ones it
// could not save.
type assets struct {
	client *http.Client
	lim    Limits

	mu      sync.Mutex
	cache   map[string]*asset
	count   int
	total   int64 // bytes kept so far, as stored (data URIs count at their base64 size)
	missing int
}

func newAssets(client *http.Client, lim Limits, pageBytes int64) *assets {
	return &assets{client: client, lim: lim, cache: map[string]*asset{}, total: pageBytes}
}

func (a *assets) missingCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.missing
}

func (a *assets) addMissing() {
	a.mu.Lock()
	a.missing++
	a.mu.Unlock()
}

// get returns the fetched asset, or nil when it was dropped or never asked for.
func (a *assets) get(u string) *asset {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.cache[u]; e != nil && e.ok {
		return e
	}
	return nil
}

// dataURI returns the asset as a data: URI, or "" when it is missing.
func (a *assets) dataURI(u string) string {
	e := a.get(u)
	if e == nil {
		return ""
	}
	return "data:" + e.ctype + ";base64," + base64.StdEncoding.EncodeToString(e.body)
}

// fetchAll fetches reqs in order, Parallel at a time, and returns when all
// are done. A URL already fetched (or failed) in an earlier batch is skipped.
func (a *assets) fetchAll(ctx context.Context, reqs []assetReq) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, a.lim.Parallel)
	for _, r := range reqs {
		a.mu.Lock()
		_, seen := a.cache[r.url]
		if !seen {
			a.cache[r.url] = &asset{}
		}
		a.mu.Unlock()
		if seen {
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			a.fetchOne(ctx, r)
		}()
	}
	wg.Wait()
}

func (a *assets) fetchOne(ctx context.Context, r assetReq) {
	a.mu.Lock()
	a.count++
	over := a.count > a.lim.MaxAssets || a.total >= a.lim.MaxTotalBytes
	a.mu.Unlock()
	if over || ctx.Err() != nil {
		a.addMissing()
		return
	}
	body, ctype, err := a.download(ctx, r)
	if err != nil {
		a.addMissing()
		return
	}
	cost := int64(len(body))
	if r.kind != kindSheet {
		cost = int64(base64.StdEncoding.EncodedLen(len(body)))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.total+cost > a.lim.MaxTotalBytes {
		a.missing++
		return
	}
	a.total += cost
	e := a.cache[r.url]
	e.body, e.ctype, e.ok = body, ctype, true
}

var errAssetRejected = errors.New("asset rejected")

func (a *assets) download(ctx context.Context, r assetReq) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", acceptFor(r.kind))
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > a.lim.MaxAssetBytes {
		return nil, "", errAssetRejected
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, a.lim.MaxAssetBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > a.lim.MaxAssetBytes {
		return nil, "", errAssetRejected
	}
	ctype, ok := checkAsset(r.kind, resp.Header.Get("Content-Type"), body)
	if !ok {
		return nil, "", errAssetRejected
	}
	return body, ctype, nil
}

func acceptFor(k assetKind) string {
	switch k {
	case kindSheet:
		return "text/css,*/*;q=0.1"
	case kindImage:
		return "image/*,*/*;q=0.8"
	}
	return "*/*"
}

// checkAsset validates the body against the kind of reference and returns
// the media type to store.
func checkAsset(k assetKind, header string, body []byte) (string, bool) {
	mt := mediaType(header)
	sniff := mediaType(http.DetectContentType(body))
	switch k {
	case kindSheet:
		if mt == "text/css" {
			return mt, true
		}
		return "text/css", mt != "text/html" && mt != "application/xhtml+xml" && sniff == "text/plain"
	case kindImage:
		if strings.HasPrefix(mt, "image/") {
			return mt, true
		}
		return sniff, strings.HasPrefix(sniff, "image/")
	}
	for _, t := range []string{mt, sniff} {
		if strings.HasPrefix(t, "font/") || strings.Contains(t, "font") || t == "application/vnd.ms-fontobject" {
			return t, true
		}
	}
	if (mt == "application/octet-stream" || mt == "binary/octet-stream") && sniff != "text/html" && sniff != "text/plain" {
		return "application/octet-stream", true
	}
	return "", false
}

// mediaType returns the lower-cased type without parameters.
func mediaType(header string) string {
	mt, _, err := mime.ParseMediaType(header)
	if err != nil {
		mt, _, _ = strings.Cut(header, ";")
		mt = strings.ToLower(strings.TrimSpace(mt))
	}
	return mt
}

// resolveAsset returns the absolute http(s) URL of ref against base, without
// its fragment. ok is false for data: URIs, fragments and other schemes.
func resolveAsset(base *url.URL, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref[0] == '#' {
		return "", false
	}
	u, err := base.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	u.Fragment = ""
	return u.String(), true
}
