package api

import (
	"net/url"
	"regexp"

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
	return out
}

func sanitizeTag(t *pb.Tag) {
	t.Color = safeColor(t.Color)
}

func sanitizeSource(s *pb.Source) {
	s.Color = safeColor(s.Color)
}
