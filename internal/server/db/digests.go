package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DigestSeries is a named, registered group of digests by one assessor.
type DigestSeries struct {
	ID           int64
	AssessorID   int64
	AssessorName string
	Name         string
	Description  string // "" is stored as NULL
	Position     int
	CreatedAt    time.Time

	// Filled by ListDigestSeries and GetDigestSeries.
	DigestCount     int
	LatestPeriodEnd *time.Time // nil when the series is empty
}

// DigestItem is an item a digest links to, with what a reader needs to show
// it.
type DigestItem struct {
	ItemID     int64
	Title      string
	Link       string
	SourceName string
	Published  *time.Time
}

// DigestRef is an earlier digest used as input.
type DigestRef struct {
	ID         int64
	Title      string
	SeriesName string
	PeriodEnd  time.Time
}

// Digest is a document an assessor wrote about one to many items.
type Digest struct {
	ID           int64
	SeriesID     int64
	SeriesName   string
	AssessorID   int64
	AssessorName string
	Title        string
	Body         string // empty in lists without withBody
	PeriodStart  time.Time
	PeriodEnd    time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Items        []*DigestItem
	Inputs       []*DigestRef
}

// NewDigest is what InsertDigest stores. The links are provenance only; the
// items and input digests must exist (the foreign keys fail otherwise).
type NewDigest struct {
	SeriesID    int64
	Title       string
	Body        string
	PeriodStart time.Time
	PeriodEnd   time.Time
	ItemIDs     []int64
	InputIDs    []int64
}

// DigestUpdate is a partial UpdateDigest: nil = unchanged. A non-nil empty
// ItemIDs or InputIDs removes every link of that kind.
type DigestUpdate struct {
	Title       *string
	Body        *string
	PeriodStart *time.Time
	PeriodEnd   *time.Time
	ItemIDs     *[]int64
	InputIDs    *[]int64
}

// ErrDigestSeriesOrder is returned by ReorderDigestSeries when the IDs are
// not exactly the set of existing series.
var ErrDigestSeriesOrder = errors.New("ids must list every digest series exactly once")

const (
	defaultDigestLimit = 20
	maxDigestLimit     = 100
)

// ── Series ──────────────────────────────────────────────────

const digestSeriesColumns = `s.id, s.assessor_id, a.name, s.name, s.description, s.position, s.created_at,
	(SELECT COUNT(*) FROM digests d WHERE d.series_id = s.id),
	(SELECT d.period_end || '' FROM digests d WHERE d.series_id = s.id ORDER BY d.period_end DESC, d.id DESC LIMIT 1)`

func scanDigestSeries(r rowScanner) (*DigestSeries, error) {
	s := &DigestSeries{}
	var desc, latest sql.NullString
	if err := r.Scan(&s.ID, &s.AssessorID, &s.AssessorName, &s.Name, &desc, &s.Position, &s.CreatedAt,
		&s.DigestCount, &latest); err != nil {
		return nil, err
	}
	s.Description = desc.String
	if latest.Valid {
		t, err := parseStoredTime(latest.String)
		if err != nil {
			return nil, err
		}
		s.LatestPeriodEnd = &t
	}
	return s, nil
}

// parseStoredTime reads the text shape the driver writes for a time
// (YYYY-MM-DD HH:MM:SS+00:00), or RFC 3339.
func parseStoredTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05-07:00", time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unreadable stored time %q", s)
}

// InsertDigestSeries creates a series at the end of the order and returns its
// ID. A duplicate (assessor, name), in any case, is a UNIQUE constraint
// error; an unknown assessor fails the foreign key.
func InsertDigestSeries(db *sql.DB, s *DigestSeries) (int64, error) {
	res, err := db.Exec(`INSERT INTO digest_series (assessor_id, name, description, position)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM digest_series))`,
		s.AssessorID, s.Name, nullableString(s.Description))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetDigestSeries returns one series, or nil when it does not exist.
func GetDigestSeries(db *sql.DB, id int64) (*DigestSeries, error) {
	s, err := scanDigestSeries(db.QueryRow(`SELECT `+digestSeriesColumns+`
		FROM digest_series s JOIN assessors a ON a.id = s.assessor_id WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// ListDigestSeries returns the series of one assessor, or of all assessors
// when assessorID is 0, ordered by position, then ID.
func ListDigestSeries(db *sql.DB, assessorID int64) ([]*DigestSeries, error) {
	q := `SELECT ` + digestSeriesColumns + ` FROM digest_series s JOIN assessors a ON a.id = s.assessor_id`
	var args []any
	if assessorID != 0 {
		q += ` WHERE s.assessor_id = ?`
		args = append(args, assessorID)
	}
	q += ` ORDER BY s.position, s.id`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*DigestSeries
	for rows.Next() {
		s, err := scanDigestSeries(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateDigestSeries writes a series' name and description (not its
// assessor or position). It reports whether the series existed.
func UpdateDigestSeries(db *sql.DB, s *DigestSeries) (bool, error) {
	res, err := db.Exec(`UPDATE digest_series SET name = ?, description = ? WHERE id = ?`,
		s.Name, nullableString(s.Description), s.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteDigestSeries removes a series and its digests. It reports whether the
// series existed.
func DeleteDigestSeries(db *sql.DB, id int64) (bool, error) {
	return deleteByID(db, `DELETE FROM digest_series WHERE id = ?`, id)
}

// ReorderDigestSeries sets the display order in one transaction. ids must be
// exactly the set of existing series, each once; otherwise nothing changes
// and ErrDigestSeriesOrder is returned.
func ReorderDigestSeries(db *sql.DB, ids []int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id FROM digest_series`)
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
		return ErrDigestSeriesOrder
	}
	got := append([]int64(nil), ids...)
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(existing, func(i, j int) bool { return existing[i] < existing[j] })
	for i := range got {
		if got[i] != existing[i] || (i > 0 && got[i] == got[i-1]) {
			return ErrDigestSeriesOrder
		}
	}
	for pos, id := range ids {
		if _, err := tx.Exec(`UPDATE digest_series SET position = ? WHERE id = ?`, pos, id); err != nil {
			return fmt.Errorf("set position of digest series %d: %w", id, err)
		}
	}
	return tx.Commit()
}

// ── Digests ─────────────────────────────────────────────────

// utcSecond is how every digest date is written: UTC, whole seconds.
func utcSecond(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// dedupIDs drops repeated IDs, keeping the first of each.
func dedupIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func writeDigestLinks(tx *sql.Tx, digestID int64, itemIDs, inputIDs []int64) error {
	for _, id := range dedupIDs(itemIDs) {
		if _, err := tx.Exec(`INSERT INTO digest_items (digest_id, item_id) VALUES (?, ?)`, digestID, id); err != nil {
			return fmt.Errorf("link item %d: %w", id, err)
		}
	}
	for _, id := range dedupIDs(inputIDs) {
		if _, err := tx.Exec(`INSERT INTO digest_inputs (digest_id, input_id) VALUES (?, ?)`, digestID, id); err != nil {
			return fmt.Errorf("link input digest %d: %w", id, err)
		}
	}
	return nil
}

// InsertDigest stores a digest and its links in one transaction and returns
// its ID. Dates are written in UTC with whole seconds; created_at and
// updated_at are now.
func InsertDigest(db *sql.DB, in *NewDigest) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	now := utcSecond(time.Now())
	res, err := tx.Exec(`INSERT INTO digests (series_id, title, body, period_start, period_end, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.SeriesID, in.Title, in.Body, utcSecond(in.PeriodStart), utcSecond(in.PeriodEnd), now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := writeDigestLinks(tx, id, in.ItemIDs, in.InputIDs); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdateDigest overwrites the given fields and replaces the given link sets
// in one transaction, and sets updated_at. It reports whether the digest
// existed.
func UpdateDigest(db *sql.DB, id int64, u *DigestUpdate) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	sets := []string{"updated_at = ?"}
	args := []any{utcSecond(time.Now())}
	if u.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *u.Title)
	}
	if u.Body != nil {
		sets = append(sets, "body = ?")
		args = append(args, *u.Body)
	}
	if u.PeriodStart != nil {
		sets = append(sets, "period_start = ?")
		args = append(args, utcSecond(*u.PeriodStart))
	}
	if u.PeriodEnd != nil {
		sets = append(sets, "period_end = ?")
		args = append(args, utcSecond(*u.PeriodEnd))
	}
	args = append(args, id)
	res, err := tx.Exec(`UPDATE digests SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if u.ItemIDs != nil {
		if _, err := tx.Exec(`DELETE FROM digest_items WHERE digest_id = ?`, id); err != nil {
			return false, err
		}
		if err := writeDigestLinks(tx, id, *u.ItemIDs, nil); err != nil {
			return false, err
		}
	}
	if u.InputIDs != nil {
		if _, err := tx.Exec(`DELETE FROM digest_inputs WHERE digest_id = ?`, id); err != nil {
			return false, err
		}
		if err := writeDigestLinks(tx, id, nil, *u.InputIDs); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// DeleteDigest removes a digest, its links, and it as an input of other
// digests. It reports whether the digest existed.
func DeleteDigest(db *sql.DB, id int64) (bool, error) {
	return deleteByID(db, `DELETE FROM digests WHERE id = ?`, id)
}

// CountDigestsByAssessor returns how many digests an assessor has written, in
// every series.
func CountDigestsByAssessor(db *sql.DB, assessorID int64) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM digests d JOIN digest_series s ON s.id = d.series_id
		WHERE s.assessor_id = ?`, assessorID).Scan(&n)
	return n, err
}

const digestColumns = `d.id, d.series_id, s.name, s.assessor_id, a.name, d.title, d.body,
	d.period_start, d.period_end, d.created_at, d.updated_at`

const digestFrom = ` FROM digests d
	JOIN digest_series s ON s.id = d.series_id
	JOIN assessors a ON a.id = s.assessor_id`

func scanDigest(r rowScanner, withBody bool) (*Digest, error) {
	d := &Digest{}
	if err := r.Scan(&d.ID, &d.SeriesID, &d.SeriesName, &d.AssessorID, &d.AssessorName, &d.Title, &d.Body,
		&d.PeriodStart, &d.PeriodEnd, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	if !withBody {
		d.Body = ""
	}
	return d, nil
}

// GetDigest returns one digest with its body, linked items and inputs, or nil
// when it does not exist.
func GetDigest(db *sql.DB, id int64) (*Digest, error) {
	d, err := scanDigest(db.QueryRow(`SELECT `+digestColumns+digestFrom+` WHERE d.id = ?`, id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := fillDigestLinks(db, []*Digest{d}); err != nil {
		return nil, err
	}
	return d, nil
}

// ListDigests returns a series' digests, newest period first (period_end,
// then ID, descending), with their linked items and inputs. beforeID is a
// cursor: the page starts after that digest in this order (0 = from the
// newest; an unknown ID gives an empty page). limit defaults to 20 and is at
// most 100. hasMore says whether older digests remain. Bodies are left empty
// unless withBody.
func ListDigests(db *sql.DB, seriesID, beforeID int64, limit int, withBody bool) ([]*Digest, bool, error) {
	if limit <= 0 {
		limit = defaultDigestLimit
	}
	if limit > maxDigestLimit {
		limit = maxDigestLimit
	}
	q := `SELECT ` + digestColumns + digestFrom + ` WHERE d.series_id = ?`
	args := []any{seriesID}
	if beforeID != 0 {
		q += ` AND (d.period_end, d.id) < (SELECT period_end, id FROM digests WHERE id = ? AND series_id = ?)`
		args = append(args, beforeID, seriesID)
	}
	q += ` ORDER BY d.period_end DESC, d.id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Digest
	for rows.Next() {
		d, err := scanDigest(rows, withBody)
		if err != nil {
			return nil, false, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	if err := fillDigestLinks(db, out); err != nil {
		return nil, false, err
	}
	return out, hasMore, nil
}

// fillDigestLinks loads the linked items and the inputs of several digests in
// one query each, like loadTagsByItemIDs.
func fillDigestLinks(db *sql.DB, digests []*Digest) error {
	if len(digests) == 0 {
		return nil
	}
	byID := make(map[int64]*Digest, len(digests))
	placeholders := make([]string, len(digests))
	args := make([]any, len(digests))
	for i, d := range digests {
		byID[d.ID] = d
		placeholders[i] = "?"
		args[i] = d.ID
	}
	in := strings.Join(placeholders, ",")

	rows, err := db.Query(`SELECT di.digest_id, i.id, i.title, i.link, s.name, i.published
		FROM digest_items di
		JOIN items i ON i.id = di.item_id
		JOIN sources s ON s.id = i.source_id
		WHERE di.digest_id IN (`+in+`)
		ORDER BY di.digest_id, i.published DESC NULLS LAST, i.id`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var digestID int64
		it := &DigestItem{}
		var published sql.NullTime
		if err := rows.Scan(&digestID, &it.ItemID, &it.Title, &it.Link, &it.SourceName, &published); err != nil {
			return err
		}
		if published.Valid {
			p := published.Time
			it.Published = &p
		}
		byID[digestID].Items = append(byID[digestID].Items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	inRows, err := db.Query(`SELECT dn.digest_id, d.id, d.title, s.name, d.period_end
		FROM digest_inputs dn
		JOIN digests d ON d.id = dn.input_id
		JOIN digest_series s ON s.id = d.series_id
		WHERE dn.digest_id IN (`+in+`)
		ORDER BY dn.digest_id, d.period_end, d.id`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = inRows.Close() }()
	for inRows.Next() {
		var digestID int64
		ref := &DigestRef{}
		if err := inRows.Scan(&digestID, &ref.ID, &ref.Title, &ref.SeriesName, &ref.PeriodEnd); err != nil {
			return err
		}
		byID[digestID].Inputs = append(byID[digestID].Inputs, ref)
	}
	return inRows.Err()
}

// FirstMissingItemID returns the first of ids (in the order given) that is not
// an item, or 0 when every one exists.
func FirstMissingItemID(db *sql.DB, ids []int64) (int64, error) {
	return firstMissingID(db, "items", ids)
}

// FirstMissingDigestID returns the first of ids that is not a digest, or 0
// when every one exists.
func FirstMissingDigestID(db *sql.DB, ids []int64) (int64, error) {
	return firstMissingID(db, "digests", ids)
}

func firstMissingID(db *sql.DB, table string, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := db.Query(`SELECT id FROM `+table+` WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	found := make(map[int64]bool, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		found[id] = true
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if !found[id] {
			return id, nil
		}
	}
	return 0, nil
}
