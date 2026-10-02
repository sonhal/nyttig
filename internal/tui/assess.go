package tui

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// Assessments in the TUI: score chips in the rows, the order of the score
// sort for live inserts, and the one-line detail of the selected item. Scores
// of different assessors are never combined: a chip is one assessor's, and
// the sort names one assessor.

// AssessorInfo describes an assessor shown in the filter bar and on chips.
type AssessorInfo struct {
	ID    int64
	Name  string
	Color string
}

// formatScore writes a score with at most two decimals and no trailing
// zeros: 0.9, 0.95, 1, 0.
func formatScore(s float64) string {
	return strconv.FormatFloat(roundTo(s, 100), 'f', -1, 64)
}

func roundTo(s, scale float64) float64 {
	return float64(int64(s*scale+0.5)) / scale
}

// subtree returns id and every tag below it, or nil for id 0 (no tag filter).
func subtree(tags []TagInfo, id int64) map[int64]bool {
	if id == 0 {
		return nil
	}
	children := childrenByParent(tags)
	seen := map[int64]bool{id: true}
	queue := []int64{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if !seen[c.ID] {
				seen[c.ID] = true
				queue = append(queue, c.ID)
			}
		}
	}
	return seen
}

// inScope reports whether an assessment counts under a tag filter (scope nil
// = no tag filter): an assessment for the item as a whole, or for a tag in
// the filter tag's subtree.
func inScope(a *pb.Assessment, scope map[int64]bool) bool {
	return scope == nil || a.TagId == 0 || scope[a.TagId]
}

// scoreOf returns the highest in-scope score the assessor gave the item.
func scoreOf(item *pb.Item, assessorID int64, scope map[int64]bool) (float64, bool) {
	best, found := 0.0, false
	for _, a := range item.Assessments {
		if a.AssessorId != assessorID || a.Score == nil || !inScope(a, scope) {
			continue
		}
		if !found || *a.Score > best {
			best, found = *a.Score, true
		}
	}
	return best, found
}

// scoreChip is one assessor's highest score for an item.
type scoreChip struct {
	assessorID int64
	name       string
	score      float64
}

// scoreChips returns one chip per assessor that scored the item (its highest
// score, whatever the tag), the selected assessor first and the others by name.
func scoreChips(item *pb.Item, selected int64) []scoreChip {
	best := map[int64]*scoreChip{}
	for _, a := range item.Assessments {
		if a.Score == nil {
			continue
		}
		if c, ok := best[a.AssessorId]; !ok || *a.Score > c.score {
			best[a.AssessorId] = &scoreChip{assessorID: a.AssessorId, name: a.AssessorName, score: *a.Score}
		}
	}
	chips := make([]scoreChip, 0, len(best))
	for _, c := range best {
		chips = append(chips, *c)
	}
	sort.Slice(chips, func(i, j int) bool {
		if (chips[i].assessorID == selected) != (chips[j].assessorID == selected) {
			return chips[i].assessorID == selected
		}
		if chips[i].name != chips[j].name {
			return chips[i].name < chips[j].name
		}
		return chips[i].assessorID < chips[j].assessorID
	})
	return chips
}

// itemOrder orders items the way the daemon sorts a list, so a live insert
// lands where a reload would put it.
type itemOrder struct {
	sort       string // newest, oldest or score
	assessorID int64  // whose scores the score sort uses
	scope      map[int64]bool
}

// before reports whether a sorts strictly before b.
func (o itemOrder) before(a, b *pb.Item) bool {
	if o.sort == "score" {
		sa, oka := scoreOf(a, o.assessorID, o.scope)
		sb, okb := scoreOf(b, o.assessorID, o.scope)
		if oka != okb {
			return oka
		}
		if oka && sa != sb {
			return sa > sb
		}
		return publishedBefore(a, b, true)
	}
	return publishedBefore(a, b, o.sort != "oldest")
}

// publishedBefore compares by published time (missing last), then fetch time.
func publishedBefore(a, b *pb.Item, newestFirst bool) bool {
	pa, pb2 := a.Published, b.Published
	switch {
	case pa == nil && pb2 == nil:
	case pa == nil:
		return false
	case pb2 == nil:
		return true
	default:
		ta, tb := pa.AsTime(), pb2.AsTime()
		if !ta.Equal(tb) {
			return ta.After(tb) == newestFirst
		}
	}
	if a.FetchedAt == nil || b.FetchedAt == nil {
		return false
	}
	fa, fb := a.FetchedAt.AsTime(), b.FetchedAt.AsTime()
	if fa.Equal(fb) {
		return false
	}
	return fa.After(fb) == newestFirst
}

// assessmentLines describes the selected item's assessments for the detail
// strip: "claude [CVE] 0.9: note". Everything is untrusted text, so it goes
// through SanitizeLine. tagNames maps tag IDs to names.
func assessmentLines(item *pb.Item, tagNames map[int64]string) []string {
	lines := make([]string, 0, len(item.Assessments))
	for _, a := range item.Assessments {
		var b strings.Builder
		b.WriteString(SanitizeLine(a.AssessorName))
		if a.TagId != 0 {
			name := tagNames[a.TagId]
			if name == "" {
				name = "#" + strconv.FormatInt(a.TagId, 10)
			}
			b.WriteString(" [" + SanitizeLine(name) + "]")
		}
		if a.Score != nil {
			b.WriteString(" " + formatScore(*a.Score))
		}
		if a.Note != "" {
			b.WriteString(": " + SanitizeLine(a.Note))
		}
		lines = append(lines, b.String())
	}
	return lines
}

// ── Rating items yourself ─────────────────────────────────────

var rateScoreRe = regexp.MustCompile(`^(\d+\.?\d*|\.\d+)$`)

const rateUsage = "a score from 0 to 1, then an optional note"

// parseRateText reads what is typed after "=": a score, then the rest of the
// line as the note.
func parseRateText(text string) (score float64, note string, err error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, "", errors.New(rateUsage)
	}
	word, rest := text, ""
	if i := strings.IndexAny(text, " \t"); i >= 0 {
		word, rest = text[:i], text[i:]
	}
	if !rateScoreRe.MatchString(word) {
		return 0, "", fmt.Errorf("%s, not %s", rateUsage, word)
	}
	score, err = strconv.ParseFloat(word, 64)
	if err != nil || score < 0 || score > 1 {
		return 0, "", fmt.Errorf("%s, not %s", rateUsage, word)
	}
	return score, strings.TrimSpace(rest), nil
}
