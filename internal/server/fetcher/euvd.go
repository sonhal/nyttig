package fetcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sonhal/nyttig/internal/server/db"
)

// TypeEUVD is the source type for a search of ENISA's EU Vulnerability
// Database (docs/euvd-plan.md). The source URL is an /api/search URL with
// the user's filters; blank means EUVDDefaultURL.
const TypeEUVD = "euvd"

// EUVDDefaultURL is what a blank EUVD source URL reads: the exploited
// vulnerabilities.
const EUVDDefaultURL = "https://euvdservices.enisa.europa.eu/api/search?exploited=true"

// EUVDDefaultName names an EUVD source added without a name.
const EUVDDefaultName = "EUVD"

// The assessors an EUVD fetch writes scores as.
const (
	EUVDCVSSAssessor = "euvd-cvss"
	EUVDEPSSAssessor = "euvd-epss"
)

const (
	// euvdWindow is how far back each fetch reads, by update date.
	euvdWindow = 14 * 24 * time.Hour
	// euvdPageSize is the API's largest page.
	euvdPageSize = 100
	// euvdMaxPages caps the requests of one fetch.
	euvdMaxPages = 20
	// euvdMaxReferences caps the reference URLs kept in a description.
	euvdMaxReferences   = 10
	euvdRecordURLPrefix = "https://euvd.enisa.europa.eu/vulnerability/"
)

// now is the fetcher's clock; tests fix it.
var now = time.Now

var (
	euvdIDRE  = regexp.MustCompile(`^EUVD-\d{4}-\d{1,12}$`)
	euvdCVERE = regexp.MustCompile(`^CVE-\d{4}-\d{4,12}$`)
)

// euvdAssessorDescriptions are the descriptions the fetcher creates the
// assessors with.
var euvdAssessorDescriptions = map[string]string{
	EUVDCVSSAssessor: "CVSS base score from the EU Vulnerability Database, divided by 10",
	EUVDEPSSAssessor: "EPSS (exploit prediction) from the EU Vulnerability Database, as a fraction",
}

// ── API types ───────────────────────────────────────────────────

type euvdPage struct {
	Items *[]euvdRecord `json:"items"`
	Total int           `json:"total"`
}

type euvdRecord struct {
	ID               string   `json:"id"`
	Description      string   `json:"description"`
	DatePublished    string   `json:"datePublished"`
	DateUpdated      string   `json:"dateUpdated"`
	BaseScore        *float64 `json:"baseScore"`
	BaseScoreVersion string   `json:"baseScoreVersion"`
	BaseScoreVector  string   `json:"baseScoreVector"`
	References       string   `json:"references"`
	Aliases          string   `json:"aliases"`
	Assigner         string   `json:"assigner"`
	EPSS             *float64 `json:"epss"`
	ExploitedSince   string   `json:"exploitedSince"`
	Products         []struct {
		Product struct {
			Name string `json:"name"`
		} `json:"product"`
		Version string `json:"product_version"`
	} `json:"enisaIdProduct"`
	Vendors []struct {
		Vendor struct {
			Name string `json:"name"`
		} `json:"vendor"`
	} `json:"enisaIdVendor"`
}

// ── Requests ────────────────────────────────────────────────────

// euvdPageURL is the request URL for one page of a source's search: the
// source URL (blank: EUVDDefaultURL) with fromUpdatedDate, size and page
// set, and its other parameters kept.
func euvdPageURL(raw string, page int, at time.Time) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = EUVDDefaultURL
	}
	// Config-seeded sources skip the service's validation, so check here.
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid euvd url: %v", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("euvd url must be an absolute http(s) url")
	}
	q := u.Query()
	q.Set("fromUpdatedDate", at.UTC().Add(-euvdWindow).Format("2006-01-02"))
	q.Set("size", strconv.Itoa(euvdPageSize))
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// fetchEUVD reads a source's search, page by page, and returns its
// entries. A non-empty note means paging stopped at the cap before the
// end; the entries read are still returned.
func fetchEUVD(client doer, src *db.Source) (entries []parsedEntry, note string, err error) {
	at := now()
	read, skipped := 0, 0
	for page := 0; page < euvdMaxPages; page++ {
		reqURL, err := euvdPageURL(src.URL, page, at)
		if err != nil {
			return nil, "", err
		}
		req, err := http.NewRequest(http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, "", fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		body, err := getBody(client, req, src.Type)
		if err != nil {
			return nil, "", err
		}
		records, total, err := parseEUVDPage(body)
		if err != nil {
			return nil, "", err
		}
		for _, r := range records {
			if e, ok := euvdEntry(r); ok {
				entries = append(entries, e)
			} else {
				skipped++
			}
		}
		read += len(records)
		if len(records) < euvdPageSize || (total > 0 && read >= total) {
			break
		}
		if page == euvdMaxPages-1 {
			matches := "more than " + strconv.Itoa(read)
			if total > read {
				matches = strconv.Itoa(total)
			}
			note = fmt.Sprintf("euvd: query matches %s records, read the first %d; narrow the query", matches, read)
		}
	}
	if skipped > 0 {
		slog.Warn("skipped euvd records without a valid id", "source_id", src.ID, "records", skipped)
	}
	return entries, note, nil
}

// parseEUVDPage reads one /api/search answer.
func parseEUVDPage(body []byte) ([]euvdRecord, int, error) {
	var p euvdPage
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, 0, fmt.Errorf("parse euvd response: %w", err)
	}
	if p.Items == nil {
		return nil, 0, errors.New("not an euvd search response: missing items")
	}
	return *p.Items, p.Total, nil
}

// ── Records ─────────────────────────────────────────────────────

// euvdEntry turns a record into an entry with its assessments. A record
// without a valid EUVD ID is skipped (ok false).
func euvdEntry(r euvdRecord) (parsedEntry, bool) {
	id := strings.TrimSpace(r.ID)
	if !euvdIDRE.MatchString(id) {
		return parsedEntry{}, false
	}
	var cves []string
	for _, a := range strings.Split(r.Aliases, "\n") {
		if a = strings.TrimSpace(a); euvdCVERE.MatchString(a) {
			cves = append(cves, a)
		}
	}
	desc := cleanText(r.Description)

	e := parsedEntry{
		GUID:   id,
		Link:   euvdRecordURLPrefix + id,
		Author: cleanText(r.Assigner),
	}
	e.Published, e.BadDate = euvdDate(r.DatePublished)

	primary := id
	if len(cves) > 0 {
		primary = cves[0]
	}
	var vendors, products []string
	for _, v := range r.Vendors {
		if n := cleanText(v.Vendor.Name); n != "" {
			vendors = appendUnique(vendors, n)
		}
	}
	for _, p := range r.Products {
		n := cleanText(p.Product.Name)
		if n == "" {
			continue
		}
		if v := cleanText(p.Version); v != "" {
			n += " " + v
		}
		products = appendUnique(products, n)
	}
	summary := firstLine(r.Description)
	if summary == "" {
		summary = strings.Join(append(append([]string{}, vendors...), products...), " ")
	}
	e.Title = primary
	if summary != "" {
		e.Title = truncateTitle(primary+": "+summary, derivedTitleMax)
	}

	parts := []string{}
	if desc != "" {
		parts = append(parts, desc)
	}
	parts = append(parts, strings.Join(append([]string{id}, cves...), ", ")+".")
	if len(vendors) > 0 {
		parts = append(parts, "Vendor: "+strings.Join(vendors, ", ")+".")
	}
	if len(products) > 0 {
		parts = append(parts, "Product: "+strings.Join(products, ", ")+".")
	}
	if a, ok := euvdCVSS(r); ok {
		parts = append(parts, a.Note+".")
		e.Assessments = append(e.Assessments, a)
	}
	if a, ok := euvdEPSS(r); ok {
		parts = append(parts, a.Note+".")
		e.Assessments = append(e.Assessments, a)
	}
	if t, _ := euvdDate(r.ExploitedSince); t != nil {
		parts = append(parts, "Exploited since "+t.Format("2006-01-02")+".")
	} else if strings.TrimSpace(r.ExploitedSince) != "" {
		parts = append(parts, "Exploited.")
	}
	if refs := euvdReferences(r.References); len(refs) > 0 {
		parts = append(parts, "References: "+strings.Join(refs, " "))
	}
	e.Description = cleanText(strings.Join(parts, " "))
	return e, true
}

// euvdCVSS is the record's CVSS base score as an assessment.
func euvdCVSS(r euvdRecord) (entryAssessment, bool) {
	if r.BaseScore == nil || *r.BaseScore < 0 || *r.BaseScore > 10 {
		return entryAssessment{}, false
	}
	note := "CVSS " + strconv.FormatFloat(*r.BaseScore, 'f', -1, 64)
	if v := cleanText(r.BaseScoreVersion); v != "" {
		note += " (" + v + ")"
	}
	if v := cleanText(r.BaseScoreVector); v != "" {
		note += " " + v
	}
	return entryAssessment{Assessor: EUVDCVSSAssessor, Score: roundScore(*r.BaseScore / 10), Note: note}, true
}

// euvdEPSS is the record's EPSS, a percentage, as an assessment.
func euvdEPSS(r euvdRecord) (entryAssessment, bool) {
	if r.EPSS == nil || *r.EPSS < 0 || *r.EPSS > 100 {
		return entryAssessment{}, false
	}
	note := "EPSS " + strconv.FormatFloat(*r.EPSS, 'f', -1, 64) + "%"
	return entryAssessment{Assessor: EUVDEPSSAssessor, Score: roundScore(*r.EPSS / 100), Note: note}, true
}

// euvdReferences are the record's http(s) reference URLs, at most
// euvdMaxReferences.
func euvdReferences(s string) []string {
	var out []string
	for _, ref := range strings.Split(s, "\n") {
		ref = strings.TrimSpace(ref)
		u, err := url.Parse(ref)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(ref, " \t") {
			continue
		}
		out = appendUnique(out, ref)
		if len(out) == euvdMaxReferences {
			break
		}
	}
	return out
}

// euvdDateLayouts are the API's date shape ("Apr 16, 2025, 7:00:16 AM", UTC).
var euvdDateLayouts = []string{
	"Jan 2, 2006, 3:04:05 PM",
	"Jan 2, 2006, 3:04:05 PM", // a narrow no-break space, as some locales format it
}

// euvdDate parses an EUVD date, then tries the feed date layouts.
func euvdDate(s string) (*time.Time, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ""
	}
	for _, layout := range euvdDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			t = normalizeTime(t)
			return &t, ""
		}
	}
	return entryDate(s)
}

// roundScore drops the float noise of a division (9.8 / 10 is
// 0.9800000000000001), keeping six decimals.
func roundScore(f float64) float64 {
	return math.Round(f*1e6) / 1e6
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}
