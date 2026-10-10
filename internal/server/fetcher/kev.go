package fetcher

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ── CISA Known Exploited Vulnerabilities ────────────────────────

// TypeKEV is the source type for CISA's Known Exploited Vulnerabilities
// catalogue: one JSON document, read as one item per newly added CVE.
const TypeKEV = "kev"

// KEVDefaultURL is CISA's own copy of the catalogue. A kev source with a
// blank URL reads it.
const KEVDefaultURL = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"

// KEVDefaultName names a kev source added without a name.
const KEVDefaultName = "CISA KEV"

// kevWindowDays bounds what a fetch inserts: only entries whose dateAdded
// is at most this many days old, so the first fetch adds about a month of
// CVEs rather than the whole catalogue back to 2021.
const kevWindowDays = 30

// kevNow is the clock the window is measured from; tests fix it.
var kevNow = time.Now

// kevCVERE is a CVE ID. The item link is built from it, so an entry whose
// ID doesn't match is skipped.
var kevCVERE = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)

// The subset of the catalogue
// (https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities_schema.json)
// the fetcher reads.
type kevCatalog struct {
	// A pointer, so a body without the array is told apart from an empty one.
	Vulnerabilities *[]kevEntry `json:"vulnerabilities"`
}

type kevEntry struct {
	CVEID                      string `json:"cveID"`
	VendorProject              string `json:"vendorProject"`
	Product                    string `json:"product"`
	VulnerabilityName          string `json:"vulnerabilityName"`
	DateAdded                  string `json:"dateAdded"`
	ShortDescription           string `json:"shortDescription"`
	RequiredAction             string `json:"requiredAction"`
	DueDate                    string `json:"dueDate"`
	KnownRansomwareCampaignUse string `json:"knownRansomwareCampaignUse"`
	Notes                      string `json:"notes"`
}

// kevRequestURL is the URL a kev source reads: its own, or CISA's when it
// has none. Config-seeded sources skip the service, which fills in the
// default, so the fetcher does it too.
func kevRequestURL(sourceURL string) string {
	if strings.TrimSpace(sourceURL) == "" {
		return KEVDefaultURL
	}
	return sourceURL
}

// parseKEV turns the catalogue into entries for the CVEs added within the
// window ending at now. It also returns how many entries it skipped for an
// invalid CVE ID or an unreadable dateAdded, for the caller's log.
func parseKEV(body []byte, now time.Time) ([]parsedEntry, int, error) {
	var catalog kevCatalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, 0, fmt.Errorf("kev parse: %w", err)
	}
	if catalog.Vulnerabilities == nil {
		return nil, 0, fmt.Errorf("kev parse: no vulnerabilities array")
	}

	// dateAdded is a day, so the window starts at midnight UTC.
	y, m, d := now.UTC().AddDate(0, 0, -kevWindowDays).Date()
	cutoff := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)

	var entries []parsedEntry
	invalid := 0
	for _, v := range *catalog.Vulnerabilities {
		id := strings.TrimSpace(v.CVEID)
		added, err := time.Parse("2006-01-02", strings.TrimSpace(v.DateAdded))
		if !kevCVERE.MatchString(id) || err != nil {
			invalid++
			continue
		}
		if added.Before(cutoff) {
			continue
		}
		entries = append(entries, parsedEntry{
			Title:       kevTitle(id, v.VulnerabilityName),
			Link:        "https://nvd.nist.gov/vuln/detail/" + id,
			Description: kevDescription(v),
			GUID:        id,
			Published:   &added,
		})
	}
	return entries, invalid, nil
}

func kevTitle(id, name string) string {
	if name = cleanText(name); name != "" {
		return id + ": " + name
	}
	return id
}

// kevDescription is the entry as one plain-text line:
// "<vendor> <product>. <description> Required action: <action> Due: <date>."
// and then the ransomware flag and the notes when there are any.
func kevDescription(v kevEntry) string {
	var parts []string
	sentence := func(s string) {
		if s = cleanText(s); s != "" {
			if !strings.HasSuffix(s, ".") {
				s += "."
			}
			parts = append(parts, s)
		}
	}
	sentence(strings.TrimSpace(cleanText(v.VendorProject) + " " + cleanText(v.Product)))
	sentence(v.ShortDescription)
	if a := cleanText(v.RequiredAction); a != "" {
		sentence("Required action: " + a)
	}
	if d := cleanText(v.DueDate); d != "" {
		sentence("Due: " + d)
	}
	if strings.EqualFold(strings.TrimSpace(v.KnownRansomwareCampaignUse), "Known") {
		parts = append(parts, "Known ransomware use.")
	}
	if n := cleanText(v.Notes); n != "" {
		parts = append(parts, "Notes: "+n)
	}
	return strings.Join(parts, " ")
}
