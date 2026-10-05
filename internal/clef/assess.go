package clef

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── Questions bound to the daemon's tags ──

// BoundQuestion is a configured question with its tag resolved against the
// daemon. TagID 0 scores the item as a whole.
type BoundQuestion struct {
	Spec  QuestionSpec
	TagID int64
}

// ID is the question's id in the request: "item" or "tag.<tag id>". Tag IDs,
// not names, so any tag name gives a valid id ([A-Za-z0-9_.-], up to 100).
func (q BoundQuestion) ID() string {
	if q.TagID == 0 {
		return "item"
	}
	return fmt.Sprintf("tag.%d", q.TagID)
}

// Wire is the question as Clef takes it.
func (q BoundQuestion) Wire() Question {
	w := Question{Type: q.Spec.Type, Instructions: q.Spec.Instructions}
	switch {
	case q.Spec.Type == TypeScore:
		w.Criteria = q.Spec.Levels
	case q.Spec.Noul != nil:
		w.Criteria = *q.Spec.Noul
	}
	return w
}

// ResolveQuestions binds each spec to its tag by name, ignoring case like the
// daemon's uniqueness rule. A tag the daemon doesn't have is an error.
func ResolveQuestions(specs []QuestionSpec, tags []*pb.Tag) ([]BoundQuestion, error) {
	byName := make(map[string]int64, len(tags))
	for _, t := range tags {
		byName[strings.ToLower(t.Name)] = t.Id
	}
	out := make([]BoundQuestion, 0, len(specs))
	for _, s := range specs {
		b := BoundQuestion{Spec: s}
		if s.Tag != "" {
			id, ok := byName[strings.ToLower(s.Tag)]
			if !ok {
				return nil, fmt.Errorf("question tag %q does not exist in the daemon", s.Tag)
			}
			b.TagID = id
		}
		out = append(out, b)
	}
	return out, nil
}

// QuestionMap is the request's questions map for the given questions.
func QuestionMap(qs []BoundQuestion) map[string]Question {
	m := make(map[string]Question, len(qs))
	for _, q := range qs {
		m[q.ID()] = q.Wire()
	}
	return m
}

// ── The tag tree ──

// TagTree holds the parent edges of the daemon's tags (a DAG: a tag can have
// several parents), refreshed from ListTags on every poll.
type TagTree struct {
	parents map[int64][]int64
}

// NewTagTree builds the tree from ListTags' tags.
func NewTagTree(tags []*pb.Tag) *TagTree {
	t := &TagTree{parents: make(map[int64][]int64, len(tags))}
	for _, tag := range tags {
		t.parents[tag.Id] = tag.ParentIds
	}
	return t
}

// ancestorsOrSelf is every tag id from which one of ids is in the subtree:
// the ids themselves and everything above them. The seen set also ends a
// cycle, which the daemon rejects but a stale snapshot could not rule out.
func (t *TagTree) ancestorsOrSelf(ids []int64) map[int64]bool {
	seen := make(map[int64]bool, len(ids))
	stack := append([]int64(nil), ids...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		if t != nil {
			stack = append(stack, t.parents[id]...)
		}
	}
	return seen
}

// Applicable are the questions to ask about an item: those with no tag, and
// those whose tag the item carries or one of its descendants (the subtree
// rule of ListItems).
func Applicable(item *pb.Item, qs []BoundQuestion, tree *TagTree) []BoundQuestion {
	ids := make([]int64, 0, len(item.Tags))
	for _, t := range item.Tags {
		ids = append(ids, t.Id)
	}
	have := tree.ancestorsOrSelf(ids)
	var out []BoundQuestion
	for _, q := range qs {
		if q.TagID == 0 || have[q.TagID] {
			out = append(out, q)
		}
	}
	return out
}

// ── State ──

// BuildState is the state sent to Clef for an item: a JSON object with the
// feed text in its own field, apart from the questions' instructions. Empty
// fields are left out and the description is cut to maxChars characters.
// The item carries its source's name, so no source list is needed.
func BuildState(item *pb.Item, maxChars int) map[string]any {
	st := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			st[k] = v
		}
	}
	set("title", item.Title)
	set("source", item.SourceName)
	if len(item.Tags) > 0 {
		names := make([]string, 0, len(item.Tags))
		for _, t := range item.Tags {
			names = append(names, t.Name)
		}
		st["tags"] = names
	}
	if item.Published != nil {
		set("published", item.Published.AsTime().UTC().Format(time.RFC3339))
	}
	set("link", item.Link)
	set("text", TruncateRunes(item.Description, maxChars))
	return st
}

// TruncateRunes cuts s to at most n characters, on a character boundary.
func TruncateRunes(s string, n int) string {
	if n < 0 || len(s) <= n { // a string of at most n bytes has at most n characters
		return s
	}
	i := 0
	for count := 0; count < n; count++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		if size == 0 {
			return s
		}
		i += size
	}
	return s[:i]
}

// ── Answers to assessments ──

// epsilon is the slack for floating-point noise at the edges of a range.
const epsilon = 1e-6

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// CheckAnswers verifies a response before anything is written: every asked
// id is present with the asked type, the numbers are finite, and noul is in
// 0 to 1 and score in 0 to (levels - 1). Answers nobody asked for are ignored.
func CheckAnswers(qs []BoundQuestion, resp *Response) error {
	if resp == nil {
		return errors.New("no response")
	}
	for _, q := range qs {
		id := q.ID()
		a, ok := resp.Answers[id]
		if !ok {
			return fmt.Errorf("question %s: no answer", id)
		}
		if a.Type != q.Spec.Type {
			return fmt.Errorf("question %s: answer type %q, asked %q", id, a.Type, q.Spec.Type)
		}
		if a.Confidence != nil && !finite(*a.Confidence) {
			return fmt.Errorf("question %s: confidence is not finite", id)
		}
		switch q.Spec.Type {
		case TypeNoul:
			if a.Noul == nil {
				return fmt.Errorf("question %s: noul answer has no noul value", id)
			}
			if !finite(*a.Noul) || *a.Noul < -epsilon || *a.Noul > 1+epsilon {
				return fmt.Errorf("question %s: noul %v is not in 0 to 1", id, *a.Noul)
			}
		case TypeScore:
			if a.Score == nil {
				return fmt.Errorf("question %s: score answer has no score value", id)
			}
			top := float64(len(q.Spec.Levels) - 1)
			if !finite(*a.Score) || *a.Score < -epsilon || *a.Score > top+epsilon {
				return fmt.Errorf("question %s: score %v is not in 0 to %v", id, *a.Score, top)
			}
		default:
			return fmt.Errorf("question %s: unsupported type %q", id, q.Spec.Type)
		}
	}
	return nil
}

// ToAssessments turns a checked response into one PutAssessment per question:
// noul as it is, score as score / (levels - 1), with a note for the latter
// that names the most likely level, the raw score and the confidence. The
// request carries the configured tag's ID, never a descendant's. A response
// that fails CheckAnswers gives an error and no requests.
func ToAssessments(item *pb.Item, assessorID int64, qs []BoundQuestion, resp *Response) ([]*pb.PutAssessmentRequest, error) {
	if err := CheckAnswers(qs, resp); err != nil {
		return nil, err
	}
	out := make([]*pb.PutAssessmentRequest, 0, len(qs))
	for _, q := range qs {
		a := resp.Answers[q.ID()]
		var score float64
		note := ""
		switch q.Spec.Type {
		case TypeNoul:
			score = *a.Noul
		case TypeScore:
			top := float64(len(q.Spec.Levels) - 1)
			score = *a.Score / top
			note = scoreNote(q.Spec.Levels, a)
		}
		score = math.Min(1, math.Max(0, score))
		out = append(out, &pb.PutAssessmentRequest{
			ItemId: item.Id, AssessorId: assessorID, TagId: q.TagID, Score: &score, Note: note,
		})
	}
	return out, nil
}

// scoreNote reads like "High (3.2/4), confidence 0.71".
func scoreNote(levels []string, a Answer) string {
	note := fmt.Sprintf("%s (%.1f/%d)", levels[likelyLevel(levels, a)], *a.Score, len(levels)-1)
	if a.Confidence != nil {
		note += fmt.Sprintf(", confidence %.2f", *a.Confidence)
	}
	return note
}

// likelyLevel is the index of the most probable level. Cloudflare's
// documentation doesn't pin down the shape of probabilities, so an array with
// one entry per level is used when that is what came, and otherwise the level
// nearest the score.
func likelyLevel(levels []string, a Answer) int {
	var probs []float64
	if len(a.Probabilities) > 0 && json.Unmarshal(a.Probabilities, &probs) == nil && len(probs) == len(levels) {
		best := -1
		for i, p := range probs {
			if finite(p) && (best < 0 || p > probs[best]) {
				best = i
			}
		}
		if best >= 0 {
			return best
		}
	}
	i := int(math.Round(*a.Score))
	return min(max(i, 0), len(levels)-1)
}
