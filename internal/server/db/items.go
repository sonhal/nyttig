package db

import (
	"database/sql"
	"strings"
	"time"
)

// ftsQuote wraps a user-supplied FTS5 query in double quotes so it is
// treated as a phrase string rather than interpreted as FTS5 query
// syntax. Embedded double-quotes are escaped by doubling them, per the
// FTS5 string syntax. An empty input is returned unchanged (no filter).
func ftsQuote(q string) string {
	if q == "" {
		return ""
	}
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}

// Item represents a news item stored in the database.
type Item struct {
	ID          int64
	SourceID    int64
	SourceName  string
	GUID        string
	Link        string
	Title       string
	Description *string
	Author      *string
	Published   *time.Time
	FetchedAt   time.Time
	Tags        []*Tag
	Viewed      bool

	// Assessments are every assessment of the item, ordered by assessor
	// name. GetItem and ListItems fill it.
	Assessments []*Assessment
}

// ItemFilter defines criteria for listing items.
type ItemFilter struct {
	SourceID     int64  // 0 = all sources
	TagID        int64  // 0 = all tags; matches the tag or any tag below it
	TagExact     bool   // match TagID only, not its descendants
	Search       string // FTS5 query, empty = no filter
	Sort         string // "newest" (default), "oldest" or "score"
	Limit        int    // default: 100
	Offset       int
	UnviewedOnly bool

	// Assessments (see docs/assessments-plan.md, "Filter semantics").
	// AssessorID selects whose scores MinScore and Sort "score" use and
	// changes nothing on its own. An assessment is in scope when the filter
	// has no tag, the assessment has no tag, or its tag is in TagID's subtree
	// (TagID itself with TagExact).
	AssessorID   int64     // 0 = none
	MinScore     *float64  // an in-scope score from AssessorID of at least this
	UnassessedBy int64     // no in-scope assessment (scored or not) by this assessor
	After        time.Time // zero = no window; else only items dated at or after it
}

// dateCutoffLayout is the shape After is bound with. items.published is
// stored as "YYYY-MM-DD HH:MM:SS+00:00" and fetched_at as
// "YYYY-MM-DD HH:MM:SS"; both compare correctly as text against a bound
// without a suffix, including within the cutoff second.
const dateCutoffLayout = "2006-01-02 15:04:05"

// InsertItem inserts a new item. Returns (itemID, inserted, error).
// inserted is false when the item already exists (UNIQUE constraint).
//
// Published is stored in UTC with whole seconds, whatever the caller passes.
// ListItems sorts the column as text, which is only chronological when every
// value has the same shape ("YYYY-MM-DD HH:MM:SS+00:00"; see migration 3).
func InsertItem(db *sql.DB, item *Item) (int64, bool, error) {
	var published *time.Time
	if item.Published != nil {
		p := item.Published.UTC().Truncate(time.Second)
		published = &p
	}
	result, err := db.Exec(`INSERT OR IGNORE INTO items (source_id, guid, link, title, description, author, published)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.SourceID, item.GUID, item.Link, item.Title, item.Description, item.Author, published)
	if err != nil {
		return 0, false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	return id, affected > 0, nil
}

// ItemIDByGUID returns the ID of a source's item with that GUID, or 0 when
// there is none.
func ItemIDByGUID(db *sql.DB, sourceID int64, guid string) (int64, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM items WHERE source_id = ? AND guid = ?`, sourceID, guid).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// GetItem retrieves a single item by ID, including source name, tags, and view status.
func GetItem(db *sql.DB, id int64) (*Item, error) {
	item := &Item{}
	var desc, author sql.NullString
	var published sql.NullTime
	var viewed int

	err := db.QueryRow(`
		SELECT i.id, i.source_id, s.name, i.guid, i.link, i.title, i.description, i.author, i.published, i.fetched_at,
			COALESCE((SELECT 1 FROM view_state WHERE item_id = i.id), 0)
		FROM items i
		JOIN sources s ON s.id = i.source_id
		WHERE i.id = ?`, id).Scan(
		&item.ID, &item.SourceID, &item.SourceName, &item.GUID, &item.Link,
		&item.Title, &desc, &author, &published, &item.FetchedAt, &viewed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		item.Description = &desc.String
	}
	if author.Valid {
		item.Author = &author.String
	}
	if published.Valid {
		item.Published = &published.Time
	}
	item.Viewed = viewed == 1

	// Load tags.
	tagRows, err := db.Query(`
		SELECT t.id, t.name, t.color
		FROM item_tags it
		JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id = ?
		ORDER BY t.name`, id)
	if err != nil {
		return nil, err
	}
	defer tagRows.Close()
	for tagRows.Next() {
		t := &Tag{}
		var color sql.NullString
		if err := tagRows.Scan(&t.ID, &t.Name, &color); err != nil {
			return nil, err
		}
		if color.Valid {
			t.Color = &color.String
		}
		item.Tags = append(item.Tags, t)
	}
	if err := tagRows.Err(); err != nil {
		return nil, err
	}

	byItem, err := loadAssessmentsByItemIDs(db, []int64{id})
	if err != nil {
		return nil, err
	}
	item.Assessments = byItem[id]

	return item, nil
}

// ListItems returns items matching the given filter criteria.
func ListItems(db *sql.DB, filter ItemFilter) ([]*Item, int, error) {
	// Defaults.
	if filter.Sort == "" {
		filter.Sort = "newest"
	}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}

	if err := validateAssessmentFilter(filter); err != nil {
		return nil, 0, err
	}

	// Build WHERE clauses and args.
	var conditions []string
	var args []interface{}

	if filter.SourceID > 0 {
		conditions = append(conditions, "i.source_id = ?")
		args = append(args, filter.SourceID)
	}
	if filter.TagID > 0 {
		if filter.TagExact {
			conditions = append(conditions, `i.id IN (SELECT item_id FROM item_tags WHERE tag_id = ?)`)
		} else {
			// IN dedups: an item tagged with two tags of the subtree is listed once.
			conditions = append(conditions, `i.id IN (SELECT item_id FROM item_tags WHERE tag_id IN (`+SubtreeSQL+`))`)
		}
		args = append(args, filter.TagID)
	}
	if filter.UnviewedOnly {
		conditions = append(conditions, "i.id NOT IN (SELECT item_id FROM view_state)")
	}
	if !filter.After.IsZero() {
		// The date the feed sorts by: published, else when it was fetched.
		conditions = append(conditions, "COALESCE(i.published, i.fetched_at) >= ?")
		args = append(args, filter.After.UTC().Format(dateCutoffLayout))
	}
	scope, scopeArgs := assessmentScopeSQL(filter)
	if filter.MinScore != nil {
		conditions = append(conditions, `i.id IN (SELECT a.item_id FROM assessments a
			WHERE a.assessor_id = ? AND a.score >= ?`+scope+`)`)
		args = append(args, filter.AssessorID, *filter.MinScore)
		args = append(args, scopeArgs...)
	}
	if filter.UnassessedBy > 0 {
		conditions = append(conditions, `i.id NOT IN (SELECT a.item_id FROM assessments a
			WHERE a.assessor_id = ?`+scope+`)`)
		args = append(args, filter.UnassessedBy)
		args = append(args, scopeArgs...)
	}
	if filter.Search != "" {
		conditions = append(conditions, "i.id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH ?)")
		args = append(args, ftsQuote(filter.Search))
	}

	// Count total first.
	countQuery := "SELECT COUNT(*) FROM items i"
	if len(conditions) > 0 {
		countQuery += " WHERE " + strings.Join(conditions, " AND ")
	}
	var total int
	if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Sort order.
	orderClause := "i.published DESC NULLS LAST, i.fetched_at DESC"
	if filter.Sort == "oldest" {
		orderClause = "i.published ASC NULLS LAST, i.fetched_at ASC"
	}

	// The score sort ranks by the highest in-scope score of the assessor;
	// items without one go last, then newest first.
	var orderArgs []interface{}
	if filter.Sort == "score" {
		orderClause = `(SELECT MAX(a.score) FROM assessments a
			WHERE a.item_id = i.id AND a.assessor_id = ?` + scope + `) DESC NULLS LAST,
			i.published DESC NULLS LAST, i.fetched_at DESC`
		orderArgs = append(orderArgs, filter.AssessorID)
		orderArgs = append(orderArgs, scopeArgs...)
	}

	// Main query with source name and view flag.
	query := `SELECT i.id, i.source_id, s.name, i.guid, i.link, i.title, i.description, i.author, i.published, i.fetched_at,
		COALESCE((SELECT 1 FROM view_state WHERE item_id = i.id), 0)
		FROM items i
		JOIN sources s ON s.id = i.source_id`

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY " + orderClause + " LIMIT ? OFFSET ?"

	allArgs := append([]interface{}{}, args...)
	allArgs = append(allArgs, orderArgs...)
	allArgs = append(allArgs, filter.Limit, filter.Offset)

	rows, err := db.Query(query, allArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []*Item
	var itemIDs []int64
	for rows.Next() {
		item := &Item{}
		var desc, author sql.NullString
		var published sql.NullTime
		var viewed int
		if err := rows.Scan(&item.ID, &item.SourceID, &item.SourceName, &item.GUID, &item.Link,
			&item.Title, &desc, &author, &published, &item.FetchedAt, &viewed); err != nil {
			return nil, 0, err
		}
		if desc.Valid {
			item.Description = &desc.String
		}
		if author.Valid {
			item.Author = &author.String
		}
		if published.Valid {
			item.Published = &published.Time
		}
		item.Viewed = viewed == 1
		items = append(items, item)
		itemIDs = append(itemIDs, item.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Batch load tags for all returned items.
	if len(itemIDs) > 0 {
		tagMap, err := loadTagsByItemIDs(db, itemIDs)
		if err != nil {
			return nil, 0, err
		}
		assessMap, err := loadAssessmentsByItemIDs(db, itemIDs)
		if err != nil {
			return nil, 0, err
		}
		for _, item := range items {
			item.Tags = tagMap[item.ID]
			item.Assessments = assessMap[item.ID]
		}
	}

	return items, total, nil
}

// loadTagsByItemIDs loads tags for multiple items in a single query.
func loadTagsByItemIDs(db *sql.DB, itemIDs []int64) (map[int64][]*Tag, error) {
	// Build IN clause.
	placeholders := make([]string, len(itemIDs))
	args := make([]interface{}, len(itemIDs))
	for i, id := range itemIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := db.Query(`
		SELECT it.item_id, t.id, t.name, t.color
		FROM item_tags it
		JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY t.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][]*Tag)
	for rows.Next() {
		var itemID int64
		t := &Tag{}
		var color sql.NullString
		if err := rows.Scan(&itemID, &t.ID, &t.Name, &color); err != nil {
			return nil, err
		}
		if color.Valid {
			t.Color = &color.String
		}
		result[itemID] = append(result[itemID], t)
	}
	return result, rows.Err()
}

// ItemMatchesSearch reports whether the item with the given ID matches the
// FTS5 search query, using the same quoting as ListItems. An empty query
// matches everything.
func ItemMatchesSearch(db *sql.DB, itemID int64, query string) (bool, error) {
	if query == "" {
		return true, nil
	}
	var one int
	err := db.QueryRow(`SELECT 1 FROM items_fts WHERE rowid = ? AND items_fts MATCH ?`,
		itemID, ftsQuote(query)).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// SearchItems performs an FTS5 search and returns matching items.
func SearchItems(db *sql.DB, query string, sourceID int64, tagID int64, limit, offset int) ([]*Item, int, error) {
	if limit <= 0 {
		limit = 100
	}

	filter := ItemFilter{
		SourceID: sourceID,
		TagID:    tagID,
		Search:   query,
		Limit:    limit,
		Offset:   offset,
	}
	return ListItems(db, filter)
}
