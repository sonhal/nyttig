package fetcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sonhal/nyttig/internal/server/db"
)

// kevToday is the fixed "now" of these tests.
var kevToday = time.Date(2026, 10, 10, 15, 30, 0, 0, time.UTC)

const kevFixture = `{
  "title": "CISA Catalog of Known Exploited Vulnerabilities",
  "catalogVersion": "2026.10.09",
  "dateReleased": "2026-10-09T17:00:00.000Z",
  "count": 7,
  "vulnerabilities": [
    {
      "cveID": "CVE-2026-12345",
      "vendorProject": "Ivanti",
      "product": "Connect Secure",
      "vulnerabilityName": "Ivanti Connect Secure Stack-Based Buffer Overflow Vulnerability",
      "dateAdded": "2026-10-09",
      "shortDescription": "Ivanti Connect Secure contains a stack-based buffer overflow that allows remote code execution.",
      "requiredAction": "Apply mitigations per vendor instructions or discontinue use of the product if mitigations are unavailable.",
      "dueDate": "2026-10-30",
      "knownRansomwareCampaignUse": "Known",
      "notes": "https://forums.ivanti.com/s/article/KB-123 ; https://nvd.nist.gov/vuln/detail/CVE-2026-12345",
      "cwes": ["CWE-121"]
    },
    {
      "cveID": "CVE-2026-0001",
      "vendorProject": "Acme",
      "product": "Widget\u009b31m",
      "vulnerabilityName": "Acme <b>Widget</b> \u001b[2J Injection",
      "dateAdded": "2026-09-10",
      "shortDescription": "Text without a full stop",
      "requiredAction": "Patch",
      "dueDate": "2026-10-01",
      "knownRansomwareCampaignUse": "Unknown",
      "notes": ""
    },
    {
      "cveID": "CVE-2026-0002",
      "vendorProject": "Old",
      "product": "Thing",
      "vulnerabilityName": "Just outside the window",
      "dateAdded": "2026-09-09"
    },
    {
      "cveID": "CVE-2021-44228",
      "vendorProject": "Apache",
      "product": "Log4j2",
      "vulnerabilityName": "Apache Log4j2 Remote Code Execution Vulnerability",
      "dateAdded": "2021-12-10"
    },
    {
      "cveID": "javascript:alert(1)",
      "vulnerabilityName": "Not a CVE ID",
      "dateAdded": "2026-10-09"
    },
    {
      "cveID": "CVE-2026-0003",
      "vulnerabilityName": "Unreadable date",
      "dateAdded": "9 October 2026"
    },
    {
      "cveID": " CVE-2026-0004 ",
      "vendorProject": "",
      "product": "",
      "vulnerabilityName": "",
      "dateAdded": "2026-10-10"
    }
  ]
}`

func TestParseKEV(t *testing.T) {
	entries, invalid, err := parseKEV([]byte(kevFixture), kevToday)
	if err != nil {
		t.Fatalf("parseKEV: %v", err)
	}
	if invalid != 2 {
		t.Errorf("invalid = %d, want 2 (bad CVE ID, bad date)", invalid)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.GUID)
	}
	// 2026-09-10 is the first day in the window (30 days before 10 October);
	// 2026-09-09 and the 2021 entry are outside it.
	if got, want := strings.Join(ids, " "), "CVE-2026-12345 CVE-2026-0001 CVE-2026-0004"; got != want {
		t.Fatalf("entries = %s, want %s", got, want)
	}

	a := entries[0]
	if want := "CVE-2026-12345: Ivanti Connect Secure Stack-Based Buffer Overflow Vulnerability"; a.Title != want {
		t.Errorf("Title = %q, want %q", a.Title, want)
	}
	if want := "https://nvd.nist.gov/vuln/detail/CVE-2026-12345"; a.Link != want {
		t.Errorf("Link = %q, want %q", a.Link, want)
	}
	wantDesc := "Ivanti Connect Secure. Ivanti Connect Secure contains a stack-based buffer overflow that allows remote code execution." +
		" Required action: Apply mitigations per vendor instructions or discontinue use of the product if mitigations are unavailable." +
		" Due: 2026-10-30. Known ransomware use." +
		" Notes: https://forums.ivanti.com/s/article/KB-123 ; https://nvd.nist.gov/vuln/detail/CVE-2026-12345"
	if a.Description != wantDesc {
		t.Errorf("Description =\n%q\nwant\n%q", a.Description, wantDesc)
	}
	if want := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC); a.Published == nil || !a.Published.Equal(want) {
		t.Errorf("Published = %v, want %v", a.Published, want)
	}
	if a.Author != "" {
		t.Errorf("Author = %q, want none", a.Author)
	}

	// Control characters are stripped; markup stays text (clients render
	// text only).
	b := entries[1]
	if want := "CVE-2026-0001: Acme <b>Widget</b> [2J Injection"; b.Title != want {
		t.Errorf("Title = %q, want %q", b.Title, want)
	}
	if want := "Acme Widget31m. Text without a full stop. Required action: Patch. Due: 2026-10-01."; b.Description != want {
		t.Errorf("Description = %q, want %q", b.Description, want)
	}

	// An entry with nothing but an ID and a date still makes an item.
	c := entries[2]
	if c.GUID != "CVE-2026-0004" || c.Title != "CVE-2026-0004" || c.Description != "" {
		t.Errorf("bare entry = %+v", c)
	}
}

func TestParseKEV_Errors(t *testing.T) {
	for _, body := range []string{
		`{"title": "not the catalogue"}`,
		`{"vulnerabilities": null}`,
		`{"vulnerabilities": "nope"}`,
		`[]`,
		`not json`,
	} {
		if _, _, err := parseKEV([]byte(body), kevToday); err == nil {
			t.Errorf("parseKEV(%s): want an error", body)
		}
	}
	entries, invalid, err := parseKEV([]byte(`{"vulnerabilities": []}`), kevToday)
	if err != nil || len(entries) != 0 || invalid != 0 {
		t.Errorf("empty catalogue = %v, %d, %v; want no entries and no error", entries, invalid, err)
	}
}

func TestBuildRequest_KEV(t *testing.T) {
	tests := []struct{ url, want string }{
		{"", KEVDefaultURL},
		{"  ", KEVDefaultURL},
		{"https://raw.githubusercontent.com/cisagov/kev-data/main/known_exploited_vulnerabilities.json", "https://raw.githubusercontent.com/cisagov/kev-data/main/known_exploited_vulnerabilities.json"},
	}
	for _, tc := range tests {
		req, err := buildRequest(&db.Source{Type: TypeKEV, URL: tc.url})
		if err != nil {
			t.Fatalf("buildRequest(%q): %v", tc.url, err)
		}
		if req.URL.String() != tc.want {
			t.Errorf("buildRequest(%q) URL = %s, want %s", tc.url, req.URL, tc.want)
		}
		if got := req.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
	}
}

func TestFetch_KEV(t *testing.T) {
	old := kevNow
	kevNow = func() time.Time { return kevToday }
	t.Cleanup(func() { kevNow = old })

	database := setupDB(t)
	t.Cleanup(func() { _ = database.Close() })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(kevFixture))
	}))
	t.Cleanup(srv.Close)

	src := &db.Source{Name: "CISA KEV", URL: srv.URL, Type: TypeKEV, RefreshSec: 3600, Enabled: true}
	id, err := db.InsertSource(database, src)
	if err != nil {
		t.Fatalf("InsertSource: %v", err)
	}
	src.ID = id

	for i, want := range []int{3, 0} {
		result, err := FetchWithClient(database, src, srv.Client())
		if err != nil {
			t.Fatalf("fetch %d: %v", i+1, err)
		}
		if result.FetchError != "" {
			t.Fatalf("fetch %d: fetch error %s", i+1, result.FetchError)
		}
		if len(result.NewItems) != want {
			t.Errorf("fetch %d: %d new items, want %d", i+1, len(result.NewItems), want)
		}
	}
	if n := countItems(t, database); n != 3 {
		t.Errorf("%d items stored, want 3", n)
	}
	var guid, published string
	if err := database.QueryRow(`SELECT guid, CAST(published AS TEXT) FROM items WHERE guid = 'CVE-2026-12345'`).Scan(&guid, &published); err != nil {
		t.Fatalf("query item: %v", err)
	}
	if published != "2026-10-09 00:00:00+00:00" {
		t.Errorf("published = %q, want the dateAdded at midnight UTC in the stored shape", published)
	}
}

func TestFetch_KEVNotACatalogue(t *testing.T) {
	database := setupDB(t)
	t.Cleanup(func() { _ = database.Close() })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss version="2.0"><channel/></rss>`))
	}))
	t.Cleanup(srv.Close)

	src := &db.Source{Name: "KEV", URL: srv.URL, Type: TypeKEV, RefreshSec: 3600, Enabled: true}
	id, err := db.InsertSource(database, src)
	if err != nil {
		t.Fatalf("InsertSource: %v", err)
	}
	src.ID = id
	result, err := FetchWithClient(database, src, srv.Client())
	if err != nil {
		t.Fatalf("FetchWithClient: %v", err)
	}
	if !strings.HasPrefix(result.FetchError, "kev parse:") {
		t.Errorf("FetchError = %q, want a kev parse error", result.FetchError)
	}
}
