package api

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// Feed content is untrusted. The browser client sanitizes again before
// rendering; this is the server-side half of that defense.

var colorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// safeLink returns link if it is an absolute http(s) URL, and "" otherwise,
// so javascript:, data: and similar URLs never reach the page as links.
func safeLink(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return link
}

// safeColor returns c if it is a #RRGGBB color, and "" otherwise.
func safeColor(c string) string {
	if colorRe.MatchString(c) {
		return c
	}
	return ""
}

// sanitizeItem returns a copy of item with an unsafe link and tag colors
// cleared. The original may be shared, so it is never modified.
func sanitizeItem(item *pb.Item) *pb.Item {
	if item == nil {
		return nil
	}
	out := proto.Clone(item).(*pb.Item)
	out.Link = safeLink(out.Link)
	for _, t := range out.Tags {
		t.Color = safeColor(t.Color)
	}
	for _, a := range out.Assessments {
		sanitizeAssessment(a)
	}
	return out
}

// safeText cleans text that an assessor wrote or that names one. Notes can
// repeat markup, links or instructions from the feed an LLM read, so the
// browser renders them as text only; this removes what has no business in
// plain text on the way out: invalid UTF-8, control characters other than
// newline and tab, and the Unicode bidirectional overrides that can make
// text read differently from how it is stored.
func safeText(s string) string {
	clean := true
	for _, r := range s {
		if !okTextRune(r) {
			clean = false
			break
		}
	}
	if clean && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(s, "\uFFFD") {
		if okTextRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func okTextRune(r rune) bool {
	switch {
	case r == '\n', r == '\t':
		return true
	case unicode.IsControl(r):
		return false
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F, r == 0x061C:
		return false
	}
	return true
}

func sanitizeAssessment(a *pb.Assessment) {
	a.AssessorName = safeText(a.AssessorName)
	a.Note = safeText(a.Note)
}

func sanitizeAssessor(a *pb.Assessor) {
	a.Name = safeText(a.Name)
	a.Description = safeText(a.Description)
	a.Color = safeColor(a.Color)
}

func sanitizeTag(t *pb.Tag) {
	t.Color = safeColor(t.Color)
}

func sanitizeSource(s *pb.Source) {
	s.Color = safeColor(s.Color)
}
