package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// SavedView is a named feed filter. A nil SourceID or TagID means the view
// does not filter on a source or tag (it is also what a deleted source or
// tag leaves behind).
type SavedView struct {
	ID           int64
	Name         string
	Search       string
	SourceID     *int64
	TagID        *int64
	Sort         string // "newest" or "oldest"
	UnviewedOnly bool
	Favorite     bool
	Position     int
}

// ErrViewOrder is returned by ReorderSavedViews when the IDs are not exactly
// the set of existing views.
var ErrViewOrder = errors.New("ids must list every saved view exactly once")

const savedViewColumns = `id, name, search, source_id, tag_id, sort, unviewed_only, favorite, position`

type rowScanner interface{ Scan(dest ...any) error }

func scanSavedView(r rowScanner) (*SavedView, error) {
	v := &SavedView{}
	var src, tag sql.NullInt64
	if err := r.Scan(&v.ID, &v.Name, &v.Search, &src, &tag, &v.Sort, &v.UnviewedOnly, &v.Favorite, &v.Position); err != nil {
		return nil, err
	}
	if src.Valid {
		v.SourceID = &src.Int64
	}
	if tag.Valid {
		v.TagID = &tag.Int64
	}
	return v, nil
}

func nullableID(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func sortOrDefault(s string) string {
	if s == "" {
		return "newest"
	}
	return s
}

// ── Saved views ─────────────────────────────────────────────

// InsertSavedView creates a view at the end of the order and returns its ID.
// A duplicate name (case-insensitive) is a UNIQUE constraint error.
func InsertSavedView(db *sql.DB, v *SavedView) (int64, error) {
	res, err := db.Exec(`INSERT INTO saved_views
		(name, search, source_id, tag_id, sort, unviewed_only, favorite, position)
		VALUES (?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM saved_views))`,
		v.Name, v.Search, nullableID(v.SourceID), nullableID(v.TagID),
		sortOrDefault(v.Sort), v.UnviewedOnly, v.Favorite)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetSavedView returns one view, or nil when it does not exist.
func GetSavedView(db *sql.DB, id int64) (*SavedView, error) {
	v, err := scanSavedView(db.QueryRow(`SELECT `+savedViewColumns+` FROM saved_views WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// ListSavedViews returns every view in display order.
func ListSavedViews(db *sql.DB) ([]*SavedView, error) {
	rows, err := db.Query(`SELECT ` + savedViewColumns + ` FROM saved_views ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var views []*SavedView
	for rows.Next() {
		v, err := scanSavedView(rows)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, rows.Err()
}

// CountSavedViews returns how many views exist.
func CountSavedViews(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM saved_views`).Scan(&n)
	return n, err
}

// UpdateSavedView writes a view's name, filter and favorite flag (not its
// position). It reports whether a view with that ID existed.
func UpdateSavedView(db *sql.DB, v *SavedView) (bool, error) {
	res, err := db.Exec(`UPDATE saved_views SET name = ?, search = ?, source_id = ?, tag_id = ?,
		sort = ?, unviewed_only = ?, favorite = ? WHERE id = ?`,
		v.Name, v.Search, nullableID(v.SourceID), nullableID(v.TagID),
		sortOrDefault(v.Sort), v.UnviewedOnly, v.Favorite, v.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteSavedView removes a view. It reports whether it existed.
func DeleteSavedView(db *sql.DB, id int64) (bool, error) {
	return deleteByID(db, `DELETE FROM saved_views WHERE id = ?`, id)
}

// ReorderSavedViews sets the display order in one transaction. ids must be
// exactly the set of existing views, each once; otherwise nothing changes
// and ErrViewOrder is returned.
func ReorderSavedViews(db *sql.DB, ids []int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id FROM saved_views`)
	if err != nil {
		return err
	}
	var existing []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		existing = append(existing, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	if len(ids) != len(existing) {
		return ErrViewOrder
	}
	got := append([]int64(nil), ids...)
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(existing, func(i, j int) bool { return existing[i] < existing[j] })
	for i := range got {
		if got[i] != existing[i] {
			return ErrViewOrder
		}
		if i > 0 && got[i] == got[i-1] {
			return ErrViewOrder
		}
	}

	for pos, id := range ids {
		if _, err := tx.Exec(`UPDATE saved_views SET position = ? WHERE id = ?`, pos, id); err != nil {
			return fmt.Errorf("set position of view %d: %w", id, err)
		}
	}
	return tx.Commit()
}

// CountViewsUsingSource returns how many views filter on a source.
func CountViewsUsingSource(db *sql.DB, sourceID int64) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM saved_views WHERE source_id = ?`, sourceID).Scan(&n)
	return n, err
}

// CountViewsUsingTag returns how many views filter on a tag.
func CountViewsUsingTag(db *sql.DB, tagID int64) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM saved_views WHERE tag_id = ?`, tagID).Scan(&n)
	return n, err
}
