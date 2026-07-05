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

// compiledRule pairs a TagRule with its pre-compiled regex pattern.
type compiledRule struct {
	rule TagRule
	re   *regexp.Regexp
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
// and skipped (matching the previous ruleMatches behavior).
func (t *Tagger) loadCompiledRules() ([]compiledRule, error) {
	rules, err := t.store.LoadRules()
	if err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	out := make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			t.logger.Warn("invalid regex pattern in rule", "rule_id", r.ID, "pattern", r.Pattern, "error", err)
			continue
		}
		out = append(out, compiledRule{rule: r, re: re})
	}
	return out, nil
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
		if !t.ruleAppliesToItem(cr.rule, item) {
			continue
		}

		if !t.matchCompiled(cr, item) {
			continue
		}

		if err := t.store.AssignTag(item.ID, cr.rule.TagID); err != nil {
			t.logger.Warn("failed to assign tag",
				"item_id", item.ID,
				"tag_id", cr.rule.TagID,
				"tag_name", cr.rule.TagName,
				"error", err,
			)
			continue
		}

		assigned++
		t.logger.Debug("tag assigned",
			"item_id", item.ID,
			"tag_id", cr.rule.TagID,
			"tag_name", cr.rule.TagName,
			"rule_id", cr.rule.ID,
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
			if !t.ruleAppliesToItem(cr.rule, item) {
				continue
			}

			if !t.matchCompiled(cr, item) {
				continue
			}

			if err := t.store.AssignTag(item.ID, cr.rule.TagID); err != nil {
				t.logger.Warn("failed to assign tag",
					"item_id", item.ID,
					"tag_id", cr.rule.TagID,
					"error", err,
				)
				continue
			}
			total++
		}
	}

	return total, nil
}

// ruleAppliesToItem returns true if the rule is global or matches the item's source.
func (t *Tagger) ruleAppliesToItem(rule TagRule, item Item) bool {
	// Global rule: SourceID == 0 applies to all sources.
	if rule.SourceID == 0 {
		return true
	}
	return rule.SourceID == item.SourceID
}

// matchCompiled tests a pre-compiled regex against the correct field(s) of the item.
func (t *Tagger) matchCompiled(cr compiledRule, item Item) bool {
	switch cr.rule.Field {
	case "title":
		return cr.re.MatchString(item.Title)
	case "description":
		return cr.re.MatchString(item.Description)
	case "both", "": // default to both
		return cr.re.MatchString(item.Title) || cr.re.MatchString(item.Description)
	default:
		t.logger.Warn("unknown field in rule, defaulting to both",
			"rule_id", cr.rule.ID,
			"field", cr.rule.Field,
		)
		return cr.re.MatchString(item.Title) || cr.re.MatchString(item.Description)
	}
}
