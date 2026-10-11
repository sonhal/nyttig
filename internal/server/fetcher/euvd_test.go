package fetcher

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sonhal/nyttig/internal/server/db"
)

// fixedNow pins the fetcher's clock for a test.
func fixedNow(t *testing.T, at time.Time) {
	t.Helper()
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old })
}

// openMigratedDB opens a database with the real migrations (the assessment
// tables are not in setupDB's hand-written schema).
func openMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "euvd.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func ptr(f float64) *float64 { return &f }

func TestEUVDPageURL(t *testing.T) {
	at := time.Date(2026, 10, 10, 1, 0, 0, 0, time.FixedZone("x", 3*3600)) // 2026-10-09 22:00 UTC
	tests := []struct {
		name, raw string
		want      map[string]string
		host      string
	}{
		{"blank is the default", "", map[string]string{"exploited": "true", "fromUpdatedDate": "2026-09-25", "size": "100", "page": "2"}, "euvdservices.enisa.europa.eu"},
		{"paging params replaced, others kept", "http://mirror.test/api/search?fromScore=9&size=5&page=7&fromUpdatedDate=2020-01-01&vendor=Microsoft",
			map[string]string{"fromScore": "9", "vendor": "Microsoft", "fromUpdatedDate": "2026-09-25", "size": "100", "page": "2"}, "mirror.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := euvdPageURL(tt.raw, 2, at)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != tt.host || u.Path != "/api/search" {
				t.Errorf("url = %s", got)
			}
			q := u.Query()
			if len(q) != len(tt.want) {
				t.Errorf("query = %v, want %v", q, tt.want)
			}
			for k, v := range tt.want {
				if q.Get(k) != v {
					t.Errorf("%s = %q, want %q", k, q.Get(k), v)
				}
			}
		})
	}
	for _, bad := range []string{"ftp://x/api/search", "/api/search", "http://"} {
		if _, err := euvdPageURL(bad, 0, at); err == nil {
			t.Errorf("euvdPageURL(%q) succeeded", bad)
		}
	}
}

func TestEUVDEntry(t *testing.T) {
	r := euvdRecord{
		ID:               "EUVD-2025-11154",
		Description:      "Buffer overflow in <b>setWan</b>\x1b[31m allows\nremote attackers to run code.",
		DatePublished:    "Apr 16, 2025, 7:00:16 AM",
		DateUpdated:      "Apr 17, 2025, 1:02:03 PM",
		BaseScore:        ptr(9.8),
		BaseScoreVersion: "3.1",
		BaseScoreVector:  "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
		References:       "https://vuldb.com/?id.1\njavascript:alert(1)\nhttps://vuldb.com/?id.1\n\nhttps://example.com/advisory\n",
		Aliases:          "CVE-2025-3675\nGHSA-xxxx\nCVE-2025-3676\n",
		Assigner:         "VulDB",
		EPSS:             ptr(0.92),
		ExploitedSince:   "Apr 9, 2025, 12:00:00 AM",
	}
	r.Products = append(r.Products, struct {
		Product struct {
			Name string `json:"name"`
		} `json:"product"`
		Version string `json:"product_version"`
	}{Version: "4.1.2"})
	r.Products[0].Product.Name = "A3700R"
	r.Vendors = append(r.Vendors, struct {
		Vendor struct {
			Name string `json:"name"`
		} `json:"vendor"`
	}{})
	r.Vendors[0].Vendor.Name = "Totolink"

	e, ok := euvdEntry(r)
	if !ok {
		t.Fatal("entry skipped")
	}
	if e.GUID != "EUVD-2025-11154" || e.Link != "https://euvd.enisa.europa.eu/vulnerability/EUVD-2025-11154" {
		t.Errorf("guid/link = %q %q", e.GUID, e.Link)
	}
	if want := "CVE-2025-3675: Buffer overflow in <b>setWan</b>[31m allows"; e.Title != want {
		t.Errorf("title = %q, want %q", e.Title, want)
	}
	if e.Author != "VulDB" {
		t.Errorf("author = %q", e.Author)
	}
	if e.Published == nil || !e.Published.Equal(time.Date(2025, 4, 16, 7, 0, 16, 0, time.UTC)) {
		t.Errorf("published = %v", e.Published)
	}
	for _, part := range []string{
		"remote attackers to run code. EUVD-2025-11154, CVE-2025-3675, CVE-2025-3676.",
		"Vendor: Totolink.", "Product: A3700R 4.1.2.",
		"CVSS 9.8 (3.1) CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H.",
		"EPSS 0.92%.", "Exploited since 2025-04-09.",
		"References: https://vuldb.com/?id.1 https://example.com/advisory",
	} {
		if !strings.Contains(e.Description, part) {
			t.Errorf("description lacks %q:\n%s", part, e.Description)
		}
	}
	if strings.ContainsAny(e.Description, "\x1b\n") || strings.Contains(e.Description, "javascript:") {
		t.Errorf("description not cleaned: %q", e.Description)
	}
	want := []entryAssessment{
		{EUVDCVSSAssessor, 0.98, "CVSS 9.8 (3.1) CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"},
		{EUVDEPSSAssessor, 0.0092, "EPSS 0.92%"},
	}
	if fmt.Sprint(e.Assessments) != fmt.Sprint(want) {
		t.Errorf("assessments = %v, want %v", e.Assessments, want)
	}
}

func TestEUVDEntry_Edges(t *testing.T) {
	for _, id := range []string{"", "CVE-2025-1", "EUVD-25-1", "EUVD-2025-1/../x", "EUVD-2025-1 "} {
		if _, ok := euvdEntry(euvdRecord{ID: id, Description: "x"}); ok != (id == "EUVD-2025-1 ") {
			t.Errorf("id %q: ok = %v", id, ok)
		}
	}
	// No CVE alias, no description, scores out of range, a bad date.
	e, ok := euvdEntry(euvdRecord{ID: "EUVD-2026-7", BaseScore: ptr(-1), EPSS: ptr(140), DatePublished: "someday",
		ExploitedSince: "unknown"})
	if !ok {
		t.Fatal("skipped")
	}
	if e.Title != "EUVD-2026-7" || len(e.Assessments) != 0 || e.Published != nil || e.BadDate != "someday" {
		t.Errorf("entry = %+v", e)
	}
	if e.Description != "EUVD-2026-7. Exploited." {
		t.Errorf("description = %q", e.Description)
	}
	// A zero score is a score; ISO dates are read too.
	e, _ = euvdEntry(euvdRecord{ID: "EUVD-2026-8", BaseScore: ptr(0), DatePublished: "2026-10-01T10:00:00Z"})
	if len(e.Assessments) != 1 || e.Assessments[0].Score != 0 || e.Published == nil {
		t.Errorf("entry = %+v", e)
	}
	// A long description makes a truncated title.
	e, _ = euvdEntry(euvdRecord{ID: "EUVD-2026-9", Aliases: "CVE-2026-0001", Description: strings.Repeat("word ", 60)})
	if n := len([]rune(e.Title)); n != derivedTitleMax || !strings.HasSuffix(e.Title, "…") {
		t.Errorf("title (%d) = %q", n, e.Title)
	}
}

func TestParseEUVDPage(t *testing.T) {
	if _, _, err := parseEUVDPage([]byte(`{"total": 3}`)); err == nil {
		t.Error("no items: want an error")
	}
	if _, _, err := parseEUVDPage([]byte(`[{"id":"EUVD-2025-1"}]`)); err == nil {
		t.Error("bare array: want an error")
	}
	recs, total, err := parseEUVDPage([]byte(`{"items": [], "total": 0}`))
	if err != nil || len(recs) != 0 || total != 0 {
		t.Errorf("empty = %v %d %v", recs, total, err)
	}
}

// euvdServer serves /api/search from records, paging like the API, and
// counts requests.
type euvdServer struct {
	mu       sync.Mutex
	records  []map[string]any
	total    int // reported total; 0 = len(records)
	status   int
	requests []url.Values
}

func (s *euvdServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.URL.Query())
	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	from, to := min(page*size, len(s.records)), min((page+1)*size, len(s.records))
	total := s.total
	if total == 0 {
		total = len(s.records)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": s.records[from:to], "total": total})
}

func euvdRecords(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{
			"id":            fmt.Sprintf("EUVD-2026-%d", i+1),
			"description":   fmt.Sprintf("Bug %d", i+1),
			"datePublished": "Oct 1, 2026, 10:00:00 AM",
			"baseScore":     7.5,
			"aliases":       fmt.Sprintf("CVE-2026-%04d\n", i+1),
		}
	}
	return out
}

func insertEUVDSource(t *testing.T, database *sql.DB, u string) *db.Source {
	t.Helper()
	src := &db.Source{Name: "EUVD", URL: u, Type: TypeEUVD, RefreshSec: 3600, Enabled: true}
	id, err := db.InsertSource(database, src)
	if err != nil {
		t.Fatal(err)
	}
	src.ID = id
	return src
}

func TestFetch_EUVD(t *testing.T) {
	fixedNow(t, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	database := openMigratedDB(t)
	srv := &euvdServer{records: euvdRecords(250)}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	src := insertEUVDSource(t, database, ts.URL+"/api/search?exploited=true")

	res, err := FetchWithClient(database, src, ts.Client())
	if err != nil || res.FetchError != "" {
		t.Fatalf("fetch: %v %q", err, res.FetchError)
	}
	if len(res.NewItems) != 250 || len(res.UpdatedItemIDs) != 0 {
		t.Fatalf("new = %d, updated = %d", len(res.NewItems), len(res.UpdatedItemIDs))
	}
	if len(srv.requests) != 3 {
		t.Errorf("requests = %d, want 3", len(srv.requests))
	}
	for i, q := range srv.requests {
		if q.Get("page") != strconv.Itoa(i) || q.Get("exploited") != "true" || q.Get("fromUpdatedDate") != "2026-09-26" {
			t.Errorf("request %d = %v", i, q)
		}
	}

	item, err := db.GetItem(database, res.NewItems[0].ID)
	if err != nil || item == nil {
		t.Fatal(err)
	}
	if item.Title != "CVE-2026-0001: Bug 1" || len(item.Assessments) != 1 ||
		item.Assessments[0].AssessorName != EUVDCVSSAssessor || *item.Assessments[0].Score != 0.75 {
		t.Errorf("item = %+v, assessments %+v", item, item.Assessments)
	}
	a, err := db.GetAssessorByName(database, EUVDCVSSAssessor)
	if err != nil || a == nil || a.Description == "" {
		t.Errorf("assessor = %+v, %v", a, err)
	}

	// An unchanged refetch writes nothing; a changed score is an update.
	srv.mu.Lock()
	srv.requests = nil
	srv.records[1]["baseScore"] = 9.1
	srv.records[2]["epss"] = 12.5
	srv.mu.Unlock()
	res, err = FetchWithClient(database, src, ts.Client())
	if err != nil || res.FetchError != "" {
		t.Fatalf("refetch: %v %q", err, res.FetchError)
	}
	if len(res.NewItems) != 0 {
		t.Errorf("refetch new = %d", len(res.NewItems))
	}
	want := []int64{res2ID(t, database, src, "EUVD-2026-2"), res2ID(t, database, src, "EUVD-2026-3")}
	if fmt.Sprint(res.UpdatedItemIDs) != fmt.Sprint(want) {
		t.Errorf("updated = %v, want %v", res.UpdatedItemIDs, want)
	}
	item, _ = db.GetItem(database, want[1])
	if len(item.Assessments) != 2 {
		t.Errorf("assessments = %+v", item.Assessments)
	}
	res, _ = FetchWithClient(database, src, ts.Client())
	if len(res.UpdatedItemIDs) != 0 {
		t.Errorf("third fetch updated = %v", res.UpdatedItemIDs)
	}
}

func res2ID(t *testing.T, database *sql.DB, src *db.Source, guid string) int64 {
	t.Helper()
	id, err := db.ItemIDByGUID(database, src.ID, guid)
	if err != nil || id == 0 {
		t.Fatalf("item %s: %d %v", guid, id, err)
	}
	return id
}

func TestFetch_EUVDPageCap(t *testing.T) {
	database := openMigratedDB(t)
	srv := &euvdServer{records: euvdRecords(euvdPageSize * (euvdMaxPages + 1))}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	src := insertEUVDSource(t, database, ts.URL+"/api/search")

	res, _ := FetchWithClient(database, src, ts.Client())
	if len(srv.requests) != euvdMaxPages || len(res.NewItems) != euvdPageSize*euvdMaxPages {
		t.Errorf("requests = %d, new = %d", len(srv.requests), len(res.NewItems))
	}
	want := "euvd: query matches 2100 records, read the first 2000; narrow the query"
	if res.FetchError != want {
		t.Errorf("fetch error = %q, want %q", res.FetchError, want)
	}
	got, _ := db.GetSource(database, src.ID)
	if got.FetchError == nil || *got.FetchError != want {
		t.Errorf("stored fetch error = %v", got.FetchError)
	}
}

func TestFetch_EUVDStopsAtTotal(t *testing.T) {
	database := openMigratedDB(t)
	// A full page whose total says there is no more: one request.
	srv := &euvdServer{records: euvdRecords(euvdPageSize)}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	src := insertEUVDSource(t, database, ts.URL+"/api/search")
	if res, _ := FetchWithClient(database, src, ts.Client()); len(res.NewItems) != euvdPageSize || len(srv.requests) != 1 {
		t.Errorf("new = %d, requests = %d", len(res.NewItems), len(srv.requests))
	}
}

func TestFetch_EUVDErrors(t *testing.T) {
	database := openMigratedDB(t)
	srv := &euvdServer{status: http.StatusForbidden}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	src := insertEUVDSource(t, database, ts.URL+"/api/search")
	if res, _ := FetchWithClient(database, src, ts.Client()); res.FetchError != "HTTP 403" {
		t.Errorf("fetch error = %q", res.FetchError)
	}

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>maintenance</html>`))
	}))
	defer ts2.Close()
	src2 := insertEUVDSource(t, database, ts2.URL+"/api/search")
	if res, _ := FetchWithClient(database, src2, ts2.Client()); !strings.HasPrefix(res.FetchError, "parse euvd response") {
		t.Errorf("fetch error = %q", res.FetchError)
	}
}
