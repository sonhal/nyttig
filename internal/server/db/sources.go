package db

import (
	"database/sql"
	"time"
)

// Source represents a feed source stored in the database.
type Source struct {
	ID           int64
	Name         string
	URL          string
	Type         string
	RefreshSec   int
	Enabled      bool
	Color        *string
	Abbreviation *string
	CreatedAt    time.Time
	LastFetch    *time.Time
	FetchError   *string
}

// InsertSource creates a new source and returns its ID.
func InsertSource(db *sql.DB, s *Source) (int64, error) {
	result, err := db.Exec(`INSERT INTO sources (name, url, type, refresh_sec, enabled, color, abbreviation)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.URL, s.Type, s.RefreshSec, s.Enabled, s.Color, s.Abbreviation)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetSource retrieves a single source by ID.
func GetSource(db *sql.DB, id int64) (*Source, error) {
	s := &Source{}
	var lastFetch sql.NullTime
	var fetchError sql.NullString
	var color sql.NullString
	var abbreviation sql.NullString
	err := db.QueryRow(`SELECT id, name, url, type, refresh_sec, enabled, color, abbreviation, created_at, last_fetch, fetch_error
		FROM sources WHERE id = ?`, id).Scan(
		&s.ID, &s.Name, &s.URL, &s.Type, &s.RefreshSec, &s.Enabled,
		&color, &abbreviation, &s.CreatedAt, &lastFetch, &fetchError)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastFetch.Valid {
		s.LastFetch = &lastFetch.Time
	}
	if fetchError.Valid {
		s.FetchError = &fetchError.String
	}
	if color.Valid {
		s.Color = &color.String
	}
	if abbreviation.Valid {
		s.Abbreviation = &abbreviation.String
	}
	return s, nil
}

// ListSources returns all sources ordered by creation time.
func ListSources(db *sql.DB) ([]*Source, error) {
	rows, err := db.Query(`SELECT id, name, url, type, refresh_sec, enabled, color, abbreviation, created_at, last_fetch, fetch_error
		FROM sources ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []*Source
	for rows.Next() {
		s := &Source{}
		var lastFetch sql.NullTime
		var fetchError sql.NullString
		var color sql.NullString
		var abbreviation sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.Type, &s.RefreshSec, &s.Enabled,
			&color, &abbreviation, &s.CreatedAt, &lastFetch, &fetchError); err != nil {
			return nil, err
		}
		if lastFetch.Valid {
			s.LastFetch = &lastFetch.Time
		}
		if fetchError.Valid {
			s.FetchError = &fetchError.String
		}
		if color.Valid {
			s.Color = &color.String
		}
		if abbreviation.Valid {
			s.Abbreviation = &abbreviation.String
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// ListEnabledSources returns only enabled sources.
func ListEnabledSources(db *sql.DB) ([]*Source, error) {
	rows, err := db.Query(`SELECT id, name, url, type, refresh_sec, enabled, color, abbreviation, created_at, last_fetch, fetch_error
		FROM sources WHERE enabled = 1 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []*Source
	for rows.Next() {
		s := &Source{}
		var lastFetch sql.NullTime
		var fetchError sql.NullString
		var color sql.NullString
		var abbreviation sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.Type, &s.RefreshSec, &s.Enabled,
			&color, &abbreviation, &s.CreatedAt, &lastFetch, &fetchError); err != nil {
			return nil, err
		}
		if color.Valid {
			s.Color = &color.String
		}
		if abbreviation.Valid {
			s.Abbreviation = &abbreviation.String
		}
		if lastFetch.Valid {
			s.LastFetch = &lastFetch.Time
		}
		if fetchError.Valid {
			s.FetchError = &fetchError.String
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// UpdateSource modifies an existing source. The ID field must be set.
func UpdateSource(db *sql.DB, s *Source) error {
	_, err := db.Exec(`UPDATE sources SET name = ?, url = ?, type = ?, refresh_sec = ?, enabled = ?, color = ?, abbreviation = ? WHERE id = ?`,
		s.Name, s.URL, s.Type, s.RefreshSec, s.Enabled, s.Color, s.Abbreviation, s.ID)
	return err
}

// DeleteSource removes a source and all its related data (cascading). It
// reports whether a source with that ID existed.
func DeleteSource(db *sql.DB, id int64) (bool, error) {
	return deleteByID(db, `DELETE FROM sources WHERE id = ?`, id)
}

// deleteByID runs a DELETE with one ID argument and reports whether it
// removed a row.
func deleteByID(db *sql.DB, query string, id int64) (bool, error) {
	res, err := db.Exec(query, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// UpdateSourceLastFetch updates the last_fetch timestamp for a source.
func UpdateSourceLastFetch(db *sql.DB, id int64, t time.Time) error {
	_, err := db.Exec(`UPDATE sources SET last_fetch = ? WHERE id = ?`, t, id)
	return err
}

// UpdateSourceFetchError records a fetch error for a source.
func UpdateSourceFetchError(db *sql.DB, id int64, errMsg string) error {
	_, err := db.Exec(`UPDATE sources SET fetch_error = ? WHERE id = ?`, errMsg, id)
	return err
}
