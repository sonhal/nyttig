package service

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sonhal/nyttig/internal/server/fetcher"
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
// source may leave blank: AddSource then names it after the account.
func validateSourceName(typ, name string) error {
	if typ == fetcher.TypeBluesky && strings.TrimSpace(name) == "" {
		return nil
	}
	return validateName("name", name, maxNameLen)
}

// validateSourceURL checks a source's url for its type. A Bluesky source
// takes an account (handle, DID or profile URL) rather than a feed URL.
func validateSourceURL(typ, raw string) error {
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
	case "rss", "atom", fetcher.TypeBluesky:
		return nil
	default:
		return fmt.Errorf("type must be rss, atom or bluesky, got %q", typ)
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
