package db

import (
	"database/sql"
	"strings"
	"time"
)

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
}

// ItemFilter defines criteria for listing items.
type ItemFilter struct {
	SourceID     int64  // 0 = all sources
	TagID        int64  // 0 = all tags
	Search       string // FTS5 query, empty = no filter
	Sort         string // "newest" (default) or "oldest"
	Limit        int    // default: 100
	Offset       int
	UnviewedOnly bool
}

// InsertItem inserts a new item. Returns (itemID, inserted, error).
// inserted is false when the item already exists (UNIQUE constraint).
func InsertItem(db *sql.DB, item *Item) (int64, bool, error) {
	result, err := db.Exec(`INSERT OR IGNORE INTO items (source_id, guid, link, title, description, author, published)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.SourceID, item.GUID, item.Link, item.Title, item.Description, item.Author, item.Published)
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

	// Build WHERE clauses and args.
	var conditions []string
	var args []interface{}

	if filter.SourceID > 0 {
		conditions = append(conditions, "i.source_id = ?")
		args = append(args, filter.SourceID)
	}
	if filter.TagID > 0 {
		conditions = append(conditions, `i.id IN (SELECT item_id FROM item_tags WHERE tag_id = ?)`)
		args = append(args, filter.TagID)
	}
	if filter.UnviewedOnly {
		conditions = append(conditions, "i.id NOT IN (SELECT item_id FROM view_state)")
	}
	if filter.Search != "" {
		conditions = append(conditions, "i.id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH ?)")
		args = append(args, filter.Search)
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
		for _, item := range items {
			item.Tags = tagMap[item.ID]
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
