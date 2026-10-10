package service

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/fetcher"
	"github.com/sonhal/nyttig/internal/since"
)

// Input limits for values that clients send to the daemon. The daemon is the
// single place these are enforced, so every client (CLI, TUI, web) gets the
// same checks. Config-file seeding writes to the database directly and is not
// subject to them; the config file is trusted input.
const (
	maxNameLen         = 200
	maxAbbreviationLen = 16
	maxTagNameLen      = 64
	maxURLLen          = 2048
	maxPatternLen      = 1024
	maxTagParents      = 16
	maxViewNameLen     = 64
	maxViewSearchLen   = 500
	maxSavedViews      = 100

	maxAssessorNameLen = 64
	maxAssessorDescLen = 500
	maxNoteBytes       = 4096

	maxSeriesNameLen   = 64
	maxDigestTitleLen  = 200
	maxDigestBodyBytes = 64 * 1024
	maxDigestItems     = 1000
	maxDigestInputs    = 100

	// minRefreshSec keeps a source from being polled more than once a minute.
	minRefreshSec = 60
	// maxRefreshSec is one week.
	maxRefreshSec = 7 * 24 * 60 * 60
	// defaultRefreshSec is used when AddSource is given no interval.
	defaultRefreshSec = 3600
)

var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// validateName checks a display name: non-empty after trimming, at most max
// characters, and free of control characters (which would corrupt TUI rows).
func validateName(field, name string, max int) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(name) > max {
		return fmt.Errorf("%s must be at most %d characters", field, max)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s must not contain control characters", field)
	}
	return nil
}

// validateFeedURL accepts only absolute http(s) URLs with a host.
func validateFeedURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	if len(raw) > maxURLLen {
		return fmt.Errorf("url must be at most %d bytes", maxURLLen)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("url must include a host")
	}
	if u.User != nil {
		return fmt.Errorf("url must not contain credentials")
	}
	return nil
}

// validateSourceName is validateName for a source's name, which a Bluesky
// or KEV source may leave blank: AddSource then names it after the account,
// or "CISA KEV".
func validateSourceName(typ, name string) error {
	if (typ == fetcher.TypeBluesky || typ == fetcher.TypeKEV) && strings.TrimSpace(name) == "" {
		return nil
	}
	return validateName("name", name, maxNameLen)
}

// validateSourceURL checks a source's url for its type. A Bluesky source
// takes an account (handle, DID or profile URL) rather than a feed URL; a
// KEV source may leave it blank for CISA's catalogue.
func validateSourceURL(typ, raw string) error {
	if typ == fetcher.TypeKEV && strings.TrimSpace(raw) == "" {
		return nil
	}
	if typ != fetcher.TypeBluesky {
		return validateFeedURL(raw)
	}
	if len(raw) > maxURLLen {
		return fmt.Errorf("url must be at most %d bytes", maxURLLen)
	}
	_, err := fetcher.ParseBlueskyActor(raw)
	return err
}

// validateFeedType accepts the feed types the fetcher understands.
func validateFeedType(typ string) error {
	switch typ {
	case "rss", "atom", fetcher.TypeBluesky, fetcher.TypeKEV:
		return nil
	default:
		return fmt.Errorf("type must be rss, atom, bluesky or kev, got %q", typ)
	}
}

// validateRefreshSec bounds the fetch interval.
func validateRefreshSec(sec int32) error {
	if sec < minRefreshSec || sec > maxRefreshSec {
		return fmt.Errorf("refresh_sec must be between %d and %d, got %d", minRefreshSec, maxRefreshSec, sec)
	}
	return nil
}

// validateColor accepts an empty string (no color) or a #RRGGBB hex color.
func validateColor(color string) error {
	if color == "" || hexColorRe.MatchString(color) {
		return nil
	}
	return fmt.Errorf("color must be a hex color like #FF6600, got %q", color)
}

// validateAbbreviation accepts an empty string (no abbreviation) or a short
// display name.
func validateAbbreviation(abbr string) error {
	if abbr == "" {
		return nil
	}
	return validateName("abbreviation", abbr, maxAbbreviationLen)
}

// validateParentIDs checks a tag's parent set: positive IDs, no duplicates,
// at most maxTagParents. Whether the tags exist, and whether the edges make
// a cycle, is the db layer's check.
func validateParentIDs(ids []int64) error {
	if len(ids) > maxTagParents {
		return fmt.Errorf("a tag can have at most %d parents, got %d", maxTagParents, len(ids))
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("parent ids must be positive, got %d", id)
		}
		if seen[id] {
			return fmt.Errorf("duplicate parent id %d", id)
		}
		seen[id] = true
	}
	return nil
}

// validateField accepts the tag-rule match fields.
func validateField(field string) error {
	switch field {
	case "title", "description", "both":
		return nil
	default:
		return fmt.Errorf("field must be title, description, or both; got %q", field)
	}
}

// compilePattern validates and compiles a tag-rule pattern. Go's regexp is
// RE2 (linear time), so user patterns cannot cause catastrophic
// backtracking; the length cap only bounds compile cost and storage.
func compilePattern(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if len(pattern) > maxPatternLen {
		return nil, fmt.Errorf("pattern must be at most %d bytes", maxPatternLen)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %v", err)
	}
	return re, nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint error.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// validateViewFilter checks the filter of a saved view: sort is "", "newest"
// or "oldest", the search text is short and free of control characters, since
// is empty or a window such as 7d, and the IDs are not negative (0 = not set). Whether the source and tag exist
// is the handler's check. A nil filter is the empty filter.
func validateViewFilter(f *pb.ViewFilter) error {
	if f == nil {
		return nil
	}
	if err := validateSort(f.Sort); err != nil {
		return err
	}
	if err := validateAssessmentFilter(f.AssessorId, f.MinScore, f.UnassessedBy, f.Sort); err != nil {
		return err
	}
	if utf8.RuneCountInString(f.Search) > maxViewSearchLen {
		return fmt.Errorf("search must be at most %d characters", maxViewSearchLen)
	}
	if strings.IndexFunc(f.Search, unicode.IsControl) >= 0 {
		return fmt.Errorf("search must not contain control characters")
	}
	if f.SourceId < 0 {
		return fmt.Errorf("source_id must not be negative, got %d", f.SourceId)
	}
	if f.TagId < 0 {
		return fmt.Errorf("tag_id must not be negative, got %d", f.TagId)
	}
	if f.Since != "" {
		if _, err := since.Parse(f.Since); err != nil {
			return err
		}
	}
	return nil
}

// validateSort accepts the sort orders a filter can ask for.
func validateSort(sort string) error {
	switch sort {
	case "", "newest", "oldest", "score":
		return nil
	default:
		return fmt.Errorf("sort must be newest, oldest or score, got %q", sort)
	}
}

// validateScore requires a finite number from 0 to 1. NaN is rejected
// explicitly: every comparison with it is false, so a range check alone
// would let it through.
func validateScore(field string, s float64) error {
	if math.IsNaN(s) || math.IsInf(s, 0) {
		return fmt.Errorf("%s must be a number from 0 to 1", field)
	}
	if s < 0 || s > 1 {
		return fmt.Errorf("%s must be between 0 and 1, got %v", field, s)
	}
	return nil
}

// validateNote accepts valid UTF-8 of at most maxNoteBytes bytes.
func validateNote(note string) error {
	if !utf8.ValidString(note) {
		return fmt.Errorf("note must be valid UTF-8")
	}
	if len(note) > maxNoteBytes {
		return fmt.Errorf("note must be at most %d bytes", maxNoteBytes)
	}
	return nil
}

// validateAssessorDescription accepts an empty string or at most
// maxAssessorDescLen characters without control characters.
func validateAssessorDescription(desc string) error {
	if utf8.RuneCountInString(desc) > maxAssessorDescLen {
		return fmt.Errorf("description must be at most %d characters", maxAssessorDescLen)
	}
	if strings.IndexFunc(desc, unicode.IsControl) >= 0 {
		return fmt.Errorf("description must not contain control characters")
	}
	return nil
}

// validateAssessmentFilter checks the assessment fields shared by SearchRequest,
// StreamFilter and ViewFilter: ids not negative, min_score a valid score, and
// an assessor named whenever min_score or the score sort needs one. Whether
// the assessor exists is only checked for saved views.
func validateAssessmentFilter(assessorID int64, minScore *float64, unassessedBy int64, sort string) error {
	if assessorID < 0 {
		return fmt.Errorf("assessor_id must not be negative, got %d", assessorID)
	}
	if unassessedBy < 0 {
		return fmt.Errorf("unassessed_by must not be negative, got %d", unassessedBy)
	}
	if minScore != nil {
		if err := validateScore("min_score", *minScore); err != nil {
			return err
		}
	}
	if (minScore != nil || sort == "score") && assessorID == 0 {
		return fmt.Errorf("min_score and sort score need an assessor_id")
	}
	return nil
}

// validateDigestBody accepts non-empty valid UTF-8 of at most
// maxDigestBodyBytes bytes without control characters other than newline and
// tab. The body is untrusted text; clients render it as text only.
func validateDigestBody(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("body is required")
	}
	if !utf8.ValidString(body) {
		return fmt.Errorf("body must be valid UTF-8")
	}
	if len(body) > maxDigestBodyBytes {
		return fmt.Errorf("body must be at most %d bytes", maxDigestBodyBytes)
	}
	if strings.IndexFunc(body, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		return fmt.Errorf("body must not contain control characters other than newline and tab")
	}
	return nil
}

// validateDigestIDs checks one list of linked IDs: positive, at most max
// after dropping duplicates. It returns the list without duplicates.
func validateDigestIDs(field string, ids []int64, max int) ([]int64, error) {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("%s must be positive, got %d", field, id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) > max {
		return nil, fmt.Errorf("%s: at most %d are allowed, got %d", field, max, len(out))
	}
	return out, nil
}
