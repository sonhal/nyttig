// Package tagger evaluates regex-based tag rules against feed items and assigns
// matching tags. It runs immediately after a feed is fetched and new items are
// inserted into the database (post-dedup).
//
// Tag rules are evaluated in priority order (lower numeric value = higher
// priority). Global rules (source_id = 0) apply to items from all sources.
// Per-source rules only apply to items from that specific source.
package tagger

import (
	"fmt"
	"log/slog"
	"regexp"
	"sort"
)

// TagRule represents a single tagging rule loaded from the database.
type TagRule struct {
	ID       int64
	SourceID int64 // 0 means global (applies to all sources)
	TagID    int64
	TagName  string
	Field    string // "title", "description", or "both"
	Pattern  string // regex pattern
	Priority int64  // lower number = evaluated first
}

// Item is a feed item that the tagger evaluates rules against.
type Item struct {
	ID          int64
	SourceID    int64
	Title       string
	Description string
}

// CompiledRule pairs a TagRule with its pre-compiled regex pattern.
type CompiledRule struct {
	Rule TagRule
	re   *regexp.Regexp
}

// BadRule is a rule whose pattern does not compile.
type BadRule struct {
	Rule TagRule
	Err  error
}

// NewCompiledRule pairs a rule with a regex its caller has already compiled
// (and validated) itself.
func NewCompiledRule(rule TagRule, re *regexp.Regexp) CompiledRule {
	return CompiledRule{Rule: rule, re: re}
}

// Compile compiles each rule's pattern, keeping the input order. Rules whose
// pattern does not compile are returned in bad instead.
func Compile(rules []TagRule) (compiled []CompiledRule, bad []BadRule) {
	compiled = make([]CompiledRule, 0, len(rules))
	for _, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			bad = append(bad, BadRule{Rule: r, Err: err})
			continue
		}
		compiled = append(compiled, CompiledRule{Rule: r, re: re})
	}
	return compiled, bad
}

// Matches reports whether the rule applies to the item's source (a global
// rule, or one for that source) and its pattern matches the rule's field.
// An unknown field matches like "both".
func (cr CompiledRule) Matches(item Item) bool {
	if cr.Rule.SourceID != 0 && cr.Rule.SourceID != item.SourceID {
		return false
	}
	return cr.MatchesText(item.Title, item.Description)
}

// MatchesText matches the rule's pattern against the rule's field, without
// checking the rule's source.
func (cr CompiledRule) MatchesText(title, description string) bool {
	switch cr.Rule.Field {
	case "title":
		return cr.re.MatchString(title)
	case "description":
		return cr.re.MatchString(description)
	default: // "both", "" or unknown
		return cr.re.MatchString(title) || cr.re.MatchString(description)
	}
}

// Desired returns, for each item that any rule matches, the IDs of the tags
// those rules give it, in ascending order without duplicates. Items no rule
// matches are left out.
func Desired(rules []CompiledRule, items []Item) map[int64][]int64 {
	out := make(map[int64][]int64)
	for _, item := range items {
		seen := make(map[int64]bool)
		for _, cr := range rules {
			if seen[cr.Rule.TagID] || !cr.Matches(item) {
				continue
			}
			seen[cr.Rule.TagID] = true
			out[item.ID] = append(out[item.ID], cr.Rule.TagID)
		}
		if tags := out[item.ID]; len(tags) > 1 {
			sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })
		}
	}
	return out
}

// RuleStore is the minimal database interface the tagger needs to load rules and
// insert tag assignments. The real implementation lives in internal/server/db/.
type RuleStore interface {
	// LoadRules returns all active tag rules, ordered by priority ascending.
	LoadRules() ([]TagRule, error)

	// AssignTag inserts an item_tags row (item_id, tag_id). Must be idempotent
	// (INSERT OR IGNORE).
	AssignTag(itemID, tagID int64) error
}

// Tagger evaluates rules against items and assigns matching tags.
type Tagger struct {
	store  RuleStore
	logger *slog.Logger
}

// New creates a Tagger that loads rules from the given store.
func New(store RuleStore, logger *slog.Logger) *Tagger {
	if logger == nil {
		logger = slog.Default()
	}
	return &Tagger{store: store, logger: logger}
}

// loadCompiledRules loads rules from the store, sorts by priority, and
// compiles each pattern. Rules whose pattern fails to compile are logged
// and skipped, and rules with an unknown field are logged (they match like
// "both").
func (t *Tagger) loadCompiledRules() ([]CompiledRule, error) {
	rules, err := t.store.LoadRules()
	if err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	compiled, bad := Compile(rules)
	for _, b := range bad {
		t.logger.Warn("invalid regex pattern in rule", "rule_id", b.Rule.ID, "pattern", b.Rule.Pattern, "error", b.Err)
	}
	for _, cr := range compiled {
		switch cr.Rule.Field {
		case "title", "description", "both", "":
		default:
			t.logger.Warn("unknown field in rule, defaulting to both",
				"rule_id", cr.Rule.ID,
				"field", cr.Rule.Field,
			)
		}
	}
	return compiled, nil
}

// TagItem evaluates all applicable rules against a single item, inserting
// matching tag assignments via the RuleStore. Rules are evaluated in priority
// order. Global rules (SourceID == 0) and per-source rules that match the
// item's SourceID are both applied.
//
// Returns the number of tags assigned.
func (t *Tagger) TagItem(item Item) (int, error) {
	compiledRules, err := t.loadCompiledRules()
	if err != nil {
		return 0, err
	}

	assigned := 0
	for _, cr := range compiledRules {
		if !cr.Matches(item) {
			continue
		}

		if err := t.store.AssignTag(item.ID, cr.Rule.TagID); err != nil {
			t.logger.Warn("failed to assign tag",
				"item_id", item.ID,
				"tag_id", cr.Rule.TagID,
				"tag_name", cr.Rule.TagName,
				"error", err,
			)
			continue
		}

		assigned++
		t.logger.Debug("tag assigned",
			"item_id", item.ID,
			"tag_id", cr.Rule.TagID,
			"tag_name", cr.Rule.TagName,
			"rule_id", cr.Rule.ID,
		)
	}

	return assigned, nil
}

// TagItems is a convenience method that calls TagItem for each item in the
// slice. Returns the total number of tags assigned.
func (t *Tagger) TagItems(items []Item) (int, error) {
	// Load and compile rules once for the batch.
	compiledRules, err := t.loadCompiledRules()
	if err != nil {
		return 0, err
	}

	total := 0
	for _, item := range items {
		for _, cr := range compiledRules {
			if !cr.Matches(item) {
				continue
			}

			if err := t.store.AssignTag(item.ID, cr.Rule.TagID); err != nil {
				t.logger.Warn("failed to assign tag",
					"item_id", item.ID,
					"tag_id", cr.Rule.TagID,
					"error", err,
				)
				continue
			}
			total++
		}
	}

	return total, nil
}
