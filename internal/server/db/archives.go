package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Archive statuses. The archives table is also the work queue: a worker takes
// the oldest pending row.
const (
	ArchivePending = "pending"
	ArchiveRunning = "running"
	ArchiveDone    = "done"
	ArchivePartial = "partial" // saved, but some assets are missing
	ArchiveFailed  = "failed"
)

const (
	defaultArchiveLimit = 50
	maxArchiveLimit     = 200
)

// Archive is a saved copy of the article behind an item. Link, Title and
// SourceName are copies, so it outlives the item.
type Archive struct {
	ID            int64
	ItemID        int64 // 0 once the item is gone
	Link          string
	Title         string
	SourceName    string
	Status        string
	Error         string
	Note          string
	FinalURL      string
	ContentType   string
	HTTPStatus    int
	Size          int64 // bytes on disk (compressed)
	OriginalSize  int64 // bytes of the page itself
	MissingAssets int
	RequestedAt   time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
}

// ArchiveRef is what an item row needs to know about its archive.
type ArchiveRef struct {
	ID     int64
	Status string
}

// ArchiveResult is what FinishArchive stores.
type ArchiveResult struct {
	Status        string // ArchiveDone, ArchivePartial or ArchiveFailed
	Error         string
	Note          string
	FinalURL      string
	ContentType   string
	HTTPStatus    int
	Size          int64
	OriginalSize  int64
	MissingAssets int
}

const archiveColumns = `a.id, a.item_id, a.link, a.title, a.source_name, a.status, a.error, a.note,
	a.final_url, a.content_type, a.http_status, a.size, a.original_size, a.missing_assets,
	a.requested_at, a.started_at, a.finished_at`

func scanArchive(r rowScanner) (*Archive, error) {
	a := &Archive{}
	var itemID sql.NullInt64
	var started, finished sql.NullTime
	if err := r.Scan(&a.ID, &itemID, &a.Link, &a.Title, &a.SourceName, &a.Status, &a.Error, &a.Note,
		&a.FinalURL, &a.ContentType, &a.HTTPStatus, &a.Size, &a.OriginalSize, &a.MissingAssets,
		&a.RequestedAt, &started, &finished); err != nil {
		return nil, err
	}
	a.ItemID = itemID.Int64
	if started.Valid {
		a.StartedAt = &started.Time
	}
	if finished.Valid {
		a.FinishedAt = &finished.Time
	}
	return a, nil
}

// EnqueueArchive queues the item's article for archiving and returns the
// archive row, or nil when the item does not exist. Link, title and source
// name are copied from the item now.
//
// An item has at most one archive. A finished one (done, partial or failed)
// goes back to pending and its old file stays until the new copy replaces
// it. One that is already pending or running is returned unchanged.
func EnqueueArchive(db *sql.DB, itemID int64) (*Archive, error) {
	_, err := db.Exec(`INSERT INTO archives (item_id, link, title, source_name, status, requested_at)
		SELECT i.id, i.link, i.title, s.name, 'pending', ?
		FROM items i JOIN sources s ON s.id = i.source_id
		WHERE i.id = ?
		ON CONFLICT(item_id) DO UPDATE SET
			link = excluded.link, title = excluded.title, source_name = excluded.source_name,
			status = 'pending', error = '', note = '', requested_at = excluded.requested_at,
			started_at = NULL, finished_at = NULL
		WHERE archives.status IN ('done', 'partial', 'failed')`,
		utcSecond(time.Now()), itemID)
	if err != nil {
		return nil, err
	}
	a, err := scanArchive(db.QueryRow(`SELECT `+archiveColumns+` FROM archives a WHERE a.item_id = ?`, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// ClaimArchive takes the oldest pending archive, marks it running and returns
// it, or returns nil when there is none.
func ClaimArchive(db *sql.DB) (*Archive, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	a, err := scanArchive(tx.QueryRow(`SELECT ` + archiveColumns + ` FROM archives a
		WHERE a.status = 'pending' ORDER BY a.requested_at, a.id LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := utcSecond(time.Now())
	if _, err := tx.Exec(`UPDATE archives SET status = 'running', started_at = ? WHERE id = ?`, now, a.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	a.Status = ArchiveRunning
	a.StartedAt = &now
	return a, nil
}

// FinishArchive stores the outcome of a running archive. It reports false when
// the archive is gone or no longer running (removed, or queued again), in
// which case nothing is written.
func FinishArchive(db *sql.DB, id int64, r ArchiveResult) (bool, error) {
	switch r.Status {
	case ArchiveDone, ArchivePartial, ArchiveFailed:
	default:
		return false, fmt.Errorf("archive status %q is not a final status", r.Status)
	}
	res, err := db.Exec(`UPDATE archives SET status = ?, error = ?, note = ?, final_url = ?, content_type = ?,
			http_status = ?, size = ?, original_size = ?, missing_assets = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`,
		r.Status, r.Error, r.Note, r.FinalURL, r.ContentType,
		r.HTTPStatus, r.Size, r.OriginalSize, r.MissingAssets, utcSecond(time.Now()), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// GetArchive returns one archive, or nil when it does not exist.
func GetArchive(db *sql.DB, id int64) (*Archive, error) {
	a, err := scanArchive(db.QueryRow(`SELECT `+archiveColumns+` FROM archives a WHERE a.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// ListArchives returns archives newest first (by ID). status "" lists every
// status; beforeID > 0 continues after a page (IDs below it). limit defaults
// to 50 and is at most 200. hasMore reports whether older ones remain.
func ListArchives(db *sql.DB, status string, beforeID int64, limit int) ([]*Archive, bool, error) {
	if limit <= 0 {
		limit = defaultArchiveLimit
	}
	if limit > maxArchiveLimit {
		limit = maxArchiveLimit
	}
	q := `SELECT ` + archiveColumns + ` FROM archives a WHERE 1 = 1`
	var args []any
	if status != "" {
		q += ` AND a.status = ?`
		args = append(args, status)
	}
	if beforeID > 0 {
		q += ` AND a.id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY a.id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Archive
	for rows.Next() {
		a, err := scanArchive(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// DeleteArchive removes the archive row. It reports whether it existed. The
// caller removes the file afterwards.
func DeleteArchive(db *sql.DB, id int64) (bool, error) {
	return deleteByID(db, `DELETE FROM archives WHERE id = ?`, id)
}

// ── Startup reconcile ───────────────────────────────────────

// RequeueRunningArchives puts archives a stopped daemon left running back to
// pending and returns how many there were.
func RequeueRunningArchives(db *sql.DB) (int, error) {
	res, err := db.Exec(`UPDATE archives SET status = 'pending', started_at = NULL WHERE status = 'running'`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ListSavedArchives returns the archives that claim a file: done and partial
// ones.
func ListSavedArchives(db *sql.DB) ([]*Archive, error) {
	rows, err := db.Query(`SELECT ` + archiveColumns + ` FROM archives a
		WHERE a.status IN ('done', 'partial') ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Archive
	for rows.Next() {
		a, err := scanArchive(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FailArchive marks a done or partial archive failed with the reason. It
// reports whether a row changed.
func FailArchive(db *sql.DB, id int64, reason string) (bool, error) {
	res, err := db.Exec(`UPDATE archives SET status = 'failed', error = ?, finished_at = ?
		WHERE id = ? AND status IN ('done', 'partial')`, reason, utcSecond(time.Now()), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ArchiveIDs returns the ID of every archive.
func ArchiveIDs(db *sql.DB) (map[int64]bool, error) {
	rows, err := db.Query(`SELECT id FROM archives`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// ── Item.Archive ────────────────────────────────────────────

// loadArchiveRefsByItemIDs returns the archive of each item that has one.
func loadArchiveRefsByItemIDs(db *sql.DB, itemIDs []int64) (map[int64]*ArchiveRef, error) {
	out := make(map[int64]*ArchiveRef)
	if len(itemIDs) == 0 {
		return out, nil
	}
	q := `SELECT item_id, id, status FROM archives WHERE item_id IN (?` + repeatPlaceholders(len(itemIDs)-1) + `)`
	args := make([]any, len(itemIDs))
	for i, id := range itemIDs {
		args[i] = id
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var itemID int64
		ref := &ArchiveRef{}
		if err := rows.Scan(&itemID, &ref.ID, &ref.Status); err != nil {
			return nil, err
		}
		out[itemID] = ref
	}
	return out, rows.Err()
}

// repeatPlaceholders returns n times ",?".
func repeatPlaceholders(n int) string {
	b := make([]byte, 0, 2*n)
	for range n {
		b = append(b, ',', '?')
	}
	return string(b)
}
