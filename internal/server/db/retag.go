package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// ── Retagging ───────────────────────────────────────────────
//
// Helpers for ApplyTagRules (docs/retag-plan.md), which re-runs the tag
// rules over stored items and makes item_tags match them. They take a
// Querier or QueryExecer so the whole sync can run in one transaction.

// QueryExecer is a Querier that can also write; *sql.DB and *sql.Tx
// satisfy it.
type QueryExecer interface {
	Querier
	Exec(query string, args ...any) (sql.Result, error)
}

// ItemTag is one item_tags row.
type ItemTag struct {
	ItemID int64
	TagID  int64
}

// TaggableItem is the part of an item the tag rules look at.
type TaggableItem struct {
	ID          int64
	SourceID    int64
	Title       string
	Description string // "" when NULL
}

// TagExists reports whether a tag with that ID exists.
func TagExists(q Querier, id int64) (bool, error) {
	return rowExists(q, `SELECT 1 FROM tags WHERE id = ?`, id)
}

// SourceExists reports whether a source with that ID exists.
func SourceExists(q Querier, id int64) (bool, error) {
	return rowExists(q, `SELECT 1 FROM sources WHERE id = ?`, id)
}

func rowExists(q Querier, query string, id int64) (bool, error) {
	var one int
	err := q.QueryRow(query, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ListTagNames returns every tag's name by ID.
func ListTagNames(q Querier) (map[int64]string, error) {
	rows, err := q.Query(`SELECT id, name FROM tags`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ListTagRulesForTag returns the rules of one tag, or of every tag when
// tagID is 0, ordered like ListTagRules.
func ListTagRulesForTag(q Querier, tagID int64) ([]*TagRule, error) {
	rows, err := q.Query(`SELECT tr.id, tr.source_id, tr.tag_id, t.name, tr.field, tr.pattern, tr.priority
		FROM tag_rules tr
		JOIN tags t ON t.id = tr.tag_id
		WHERE ? = 0 OR tr.tag_id = ?
		ORDER BY tr.priority ASC, tr.id ASC`, tagID, tagID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var rules []*TagRule
	for rows.Next() {
		r := &TagRule{}
		var srcID sql.NullInt64
		if err := rows.Scan(&r.ID, &srcID, &r.TagID, &r.TagName, &r.Field, &r.Pattern, &r.Priority); err != nil {
			return nil, err
		}
		if srcID.Valid {
			r.SourceID = &srcID.Int64
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// ListItemsForTagging returns the fields the tag rules match on for every
// item, or for one source's items when sourceID is not 0, ordered by ID.
func ListItemsForTagging(q Querier, sourceID int64) ([]TaggableItem, error) {
	rows, err := q.Query(`SELECT id, source_id, title, COALESCE(description, '')
		FROM items
		WHERE ? = 0 OR source_id = ?
		ORDER BY id`, sourceID, sourceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []TaggableItem
	for rows.Next() {
		var it TaggableItem
		if err := rows.Scan(&it.ID, &it.SourceID, &it.Title, &it.Description); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// ListItemTagPairs returns the item_tags rows of one tag (every tag when
// tagID is 0), limited to one source's items when sourceID is not 0.
func ListItemTagPairs(q Querier, tagID, sourceID int64) ([]ItemTag, error) {
	rows, err := q.Query(`SELECT it.item_id, it.tag_id
		FROM item_tags it
		JOIN items i ON i.id = it.item_id
		WHERE (? = 0 OR it.tag_id = ?) AND (? = 0 OR i.source_id = ?)
		ORDER BY it.item_id, it.tag_id`, tagID, tagID, sourceID, sourceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var pairs []ItemTag
	for rows.Next() {
		var p ItemTag
		if err := rows.Scan(&p.ItemID, &p.TagID); err != nil {
			return nil, err
		}
		pairs = append(pairs, p)
	}
	return pairs, rows.Err()
}

// AssignTagsToItems inserts item_tags rows, ignoring ones that exist.
func AssignTagsToItems(q QueryExecer, pairs []ItemTag) error {
	for _, p := range pairs {
		if _, err := q.Exec(`INSERT OR IGNORE INTO item_tags (item_id, tag_id) VALUES (?, ?)`, p.ItemID, p.TagID); err != nil {
			return fmt.Errorf("assign tag %d to item %d: %w", p.TagID, p.ItemID, err)
		}
	}
	return nil
}

// RemoveTagsFromItems deletes item_tags rows; missing ones are ignored.
func RemoveTagsFromItems(q QueryExecer, pairs []ItemTag) error {
	for _, p := range pairs {
		if _, err := q.Exec(`DELETE FROM item_tags WHERE item_id = ? AND tag_id = ?`, p.ItemID, p.TagID); err != nil {
			return fmt.Errorf("remove tag %d from item %d: %w", p.TagID, p.ItemID, err)
		}
	}
	return nil
}
