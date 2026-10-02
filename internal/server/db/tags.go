package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// Tag represents a user-defined tag for categorizing items.
type Tag struct {
	ID    int64
	Name  string
	Color *string // hex color, e.g. "#FF6B35", can be NULL

	// ParentIDs are the tag's direct parents, empty for a top-level tag.
	// ListTags fills it; GetTag and GetTagsForItem leave it nil.
	ParentIDs []int64
}

// TagEdge is one row of tag_parents: Child sits directly under Parent.
type TagEdge struct {
	ChildID  int64
	ParentID int64
}

// Querier is the part of *sql.DB and *sql.Tx the tag-tree queries need.
type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// ErrTagCycle is returned when a parent edge would make a tag its own
// ancestor.
type ErrTagCycle struct {
	Child, Parent string // tag names; equal for a self-loop
}

func (e *ErrTagCycle) Error() string {
	if e.Child == e.Parent {
		return fmt.Sprintf("tag %q cannot be its own parent", e.Child)
	}
	return fmt.Sprintf("%q is already an ancestor of %q", e.Child, e.Parent)
}

// ErrTagNotFound is returned when a tag ID that a call refers to (a child or
// a parent) does not exist.
type ErrTagNotFound struct{ ID int64 }

func (e *ErrTagNotFound) Error() string { return fmt.Sprintf("tag %d not found", e.ID) }

// SubtreeSQL selects the IDs of a tag and all its descendants; it takes the
// tag ID as its one argument. UNION (not UNION ALL) dedups, so a tag reached
// by two paths appears once and the walk ends even if a cycle ever got into
// the table.
const SubtreeSQL = `WITH RECURSIVE subtree(id) AS (
	SELECT ?
	UNION
	SELECT tp.child_id FROM tag_parents tp JOIN subtree s ON tp.parent_id = s.id
) SELECT id FROM subtree`

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

// ListTags returns all tags ordered by name, with their direct parents.
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close() // the pool has one connection: free it for the edge query

	edges, err := ListTagEdges(db)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Tag, len(tags))
	for _, t := range tags {
		byID[t.ID] = t
	}
	for _, e := range edges {
		if t := byID[e.ChildID]; t != nil {
			t.ParentIDs = append(t.ParentIDs, e.ParentID)
		}
	}
	return tags, nil
}

// DeleteTag removes a tag and all its associated rules, item_tags and parent
// edges (cascading). Its children are kept; one with no other parent becomes
// top-level. It reports whether a tag with that ID existed.
func DeleteTag(db *sql.DB, id int64) (bool, error) {
	return deleteByID(db, `DELETE FROM tags WHERE id = ?`, id)
}

// ── Tag tree ────────────────────────────────────────────────

// ListTagEdges returns every (child, parent) pair, ordered by child and
// parent.
func ListTagEdges(q Querier) ([]TagEdge, error) {
	rows, err := q.Query(`SELECT child_id, parent_id FROM tag_parents ORDER BY child_id, parent_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var edges []TagEdge
	for rows.Next() {
		var e TagEdge
		if err := rows.Scan(&e.ChildID, &e.ParentID); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// TagDescendants returns id and every tag below it, in no particular order.
func TagDescendants(q Querier, id int64) ([]int64, error) {
	rows, err := q.Query(SubtreeSQL, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var d int64
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		ids = append(ids, d)
	}
	return ids, rows.Err()
}

// SetTagParents replaces a tag's parent set. The cycle check and the writes
// share one transaction; the daemon uses one connection, so two edits cannot
// each pass the check and together form a cycle. It returns *ErrTagNotFound
// for an unknown child or parent and *ErrTagCycle for a cycle; nothing is
// written then.
func SetTagParents(db *sql.DB, childID int64, parentIDs []int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := setTagParents(tx, childID, parentIDs); err != nil {
		return err
	}
	return tx.Commit()
}

// InsertTagWithParents creates a tag and sets its parents in one
// transaction, so a rejected parent leaves no tag behind.
func InsertTagWithParents(db *sql.DB, name string, color *string, parentIDs []int64) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.Exec(`INSERT INTO tags (name, color) VALUES (?, ?)`, name, color)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := setTagParents(tx, id, parentIDs); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// tagTx is what setTagParents works through; *sql.Tx satisfies it.
type tagTx interface {
	Querier
	Exec(query string, args ...any) (sql.Result, error)
}

func setTagParents(tx tagTx, childID int64, parentIDs []int64) error {
	childName, err := tagName(tx, childID)
	if err != nil {
		return err
	}
	// Changing a tag's parents never changes what sits below it, so one
	// subtree lookup checks every new edge.
	below, err := TagDescendants(tx, childID)
	if err != nil {
		return err
	}
	isBelow := make(map[int64]bool, len(below))
	for _, d := range below {
		isBelow[d] = true
	}
	for _, p := range parentIDs {
		parentName, err := tagName(tx, p)
		if err != nil {
			return err
		}
		if p == childID || isBelow[p] {
			return &ErrTagCycle{Child: childName, Parent: parentName}
		}
	}

	if _, err := tx.Exec(`DELETE FROM tag_parents WHERE child_id = ?`, childID); err != nil {
		return err
	}
	for _, p := range parentIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tag_parents (child_id, parent_id) VALUES (?, ?)`, childID, p); err != nil {
			return err
		}
	}
	return nil
}

// tagName returns a tag's name, or *ErrTagNotFound.
func tagName(q Querier, id int64) (string, error) {
	var name string
	err := q.QueryRow(`SELECT name FROM tags WHERE id = ?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &ErrTagNotFound{ID: id}
	}
	return name, err
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

// DeleteTagRule removes a tag rule by ID. It reports whether a rule with
// that ID existed.
func DeleteTagRule(db *sql.DB, id int64) (bool, error) {
	return deleteByID(db, `DELETE FROM tag_rules WHERE id = ?`, id)
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

// UpdateTag writes a tag's name and color. Rules and item assignments
// reference the tag by ID, so they are kept.
func UpdateTag(db *sql.DB, t *Tag) error {
	_, err := db.Exec(`UPDATE tags SET name = ?, color = ? WHERE id = ?`, t.Name, t.Color, t.ID)
	return err
}
