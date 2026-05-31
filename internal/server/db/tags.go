package db

import (
	"database/sql"
)

// Tag represents a user-defined tag for categorizing items.
type Tag struct {
	ID    int64
	Name  string
	Color *string // hex color, e.g. "#FF6B35", can be NULL
}

// TagRule represents a regex rule for auto-tagging items.
type TagRule struct {
	ID       int64
	SourceID *int64 // nil = global rule
	TagID    int64
	TagName  string
	Field    string // title, description, both
	Pattern  string // regex pattern
	Priority int
}

// ── Tags ────────────────────────────────────────────────────

// InsertTag creates a new tag and returns its ID.
func InsertTag(db *sql.DB, name string, color *string) (int64, error) {
	result, err := db.Exec(`INSERT INTO tags (name, color) VALUES (?, ?)`, name, color)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetTag retrieves a single tag by ID.
func GetTag(db *sql.DB, id int64) (*Tag, error) {
	t := &Tag{}
	var color sql.NullString
	err := db.QueryRow(`SELECT id, name, color FROM tags WHERE id = ?`, id).Scan(
		&t.ID, &t.Name, &color)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if color.Valid {
		t.Color = &color.String
	}
	return t, nil
}

// ListTags returns all tags ordered by name.
func ListTags(db *sql.DB) ([]*Tag, error) {
	rows, err := db.Query(`SELECT id, name, color FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []*Tag
	for rows.Next() {
		t := &Tag{}
		var color sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &color); err != nil {
			return nil, err
		}
		if color.Valid {
			t.Color = &color.String
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// DeleteTag removes a tag and all its associated rules and item_tags (cascading).
func DeleteTag(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM tags WHERE id = ?`, id)
	return err
}

// ── Tag Rules ───────────────────────────────────────────────

// InsertTagRule creates a new tag rule and returns its ID.
func InsertTagRule(db *sql.DB, rule *TagRule) (int64, error) {
	result, err := db.Exec(`INSERT INTO tag_rules (source_id, tag_id, field, pattern, priority)
		VALUES (?, ?, ?, ?, ?)`,
		rule.SourceID, rule.TagID, rule.Field, rule.Pattern, rule.Priority)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetTagRule retrieves a single tag rule by ID, including the tag name.
func GetTagRule(db *sql.DB, id int64) (*TagRule, error) {
	r := &TagRule{}
	var sourceID sql.NullInt64
	err := db.QueryRow(`SELECT tr.id, tr.source_id, tr.tag_id, t.name, tr.field, tr.pattern, tr.priority
		FROM tag_rules tr
		JOIN tags t ON t.id = tr.tag_id
		WHERE tr.id = ?`, id).Scan(
		&r.ID, &sourceID, &r.TagID, &r.TagName, &r.Field, &r.Pattern, &r.Priority)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if sourceID.Valid {
		r.SourceID = &sourceID.Int64
	}
	return r, nil
}

// ListTagRules returns tag rules, optionally filtered by source_id.
// sourceID == nil: return all rules; non-nil: only rules where source_id matches or is NULL (global).
func ListTagRules(db *sql.DB, sourceID *int64) ([]*TagRule, error) {
	var rows *sql.Rows
	var err error

	if sourceID == nil {
		rows, err = db.Query(`SELECT tr.id, tr.source_id, tr.tag_id, t.name, tr.field, tr.pattern, tr.priority
			FROM tag_rules tr
			JOIN tags t ON t.id = tr.tag_id
			ORDER BY tr.priority ASC, tr.id ASC`)
	} else {
		rows, err = db.Query(`SELECT tr.id, tr.source_id, tr.tag_id, t.name, tr.field, tr.pattern, tr.priority
			FROM tag_rules tr
			JOIN tags t ON t.id = tr.tag_id
			WHERE tr.source_id = ? OR tr.source_id IS NULL
			ORDER BY tr.priority ASC, tr.id ASC`, *sourceID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// DeleteTagRule removes a tag rule by ID.
func DeleteTagRule(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM tag_rules WHERE id = ?`, id)
	return err
}

// ── Item-Tag Association ────────────────────────────────────

// AssignTagToItem associates a tag with an item (idempotent).
func AssignTagToItem(db *sql.DB, itemID, tagID int64) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO item_tags (item_id, tag_id) VALUES (?, ?)`, itemID, tagID)
	return err
}

// RemoveTagFromItem removes a tag association from an item.
func RemoveTagFromItem(db *sql.DB, itemID, tagID int64) error {
	_, err := db.Exec(`DELETE FROM item_tags WHERE item_id = ? AND tag_id = ?`, itemID, tagID)
	return err
}

// GetTagsForItem returns all tags associated with an item.
func GetTagsForItem(db *sql.DB, itemID int64) ([]*Tag, error) {
	rows, err := db.Query(`SELECT t.id, t.name, t.color
		FROM item_tags it
		JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id = ?
		ORDER BY t.name`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []*Tag
	for rows.Next() {
		t := &Tag{}
		var color sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &color); err != nil {
			return nil, err
		}
		if color.Valid {
			t.Color = &color.String
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}
