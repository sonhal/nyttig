package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// MarkViewed inserts view records for the given item IDs (idempotent).
func MarkViewed(db *sql.DB, itemIDs []int64) error {
	if len(itemIDs) == 0 {
		return nil
	}

	// Build multi-value INSERT OR IGNORE.
	placeholders := make([]string, len(itemIDs))
	args := make([]interface{}, len(itemIDs))
	for i, id := range itemIDs {
		placeholders[i] = "(?)"
		args[i] = id
	}

	query := fmt.Sprintf("INSERT OR IGNORE INTO view_state (item_id) VALUES %s",
		strings.Join(placeholders, ", "))

	_, err := db.Exec(query, args...)
	return err
}

// IsViewed checks whether a single item has been viewed.
func IsViewed(db *sql.DB, itemID int64) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM view_state WHERE item_id = ?`, itemID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// CountUnviewed returns the number of items that have NOT been viewed.
func CountUnviewed(db *sql.DB) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE id NOT IN (SELECT item_id FROM view_state)`).Scan(&count)
	return count, err
}

// CountUnviewedBySource returns unviewed count for a specific source (0 = all).
func CountUnviewedBySource(db *sql.DB, sourceID int64) (int, error) {
	var count int
	var err error
	if sourceID == 0 {
		err = db.QueryRow(`SELECT COUNT(*) FROM items WHERE id NOT IN (SELECT item_id FROM view_state)`).Scan(&count)
	} else {
		err = db.QueryRow(`SELECT COUNT(*) FROM items WHERE source_id = ? AND id NOT IN (SELECT item_id FROM view_state)`, sourceID).Scan(&count)
	}
	return count, err
}
