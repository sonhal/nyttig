package db

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Assessor is a registered system (a model, a CVE reader, you) that writes
// assessments. Its Description says what its scores mean.
type Assessor struct {
	ID          int64
	Name        string
	Description string // "" is stored as NULL
	Color       string // hex color, "" is stored as NULL
	CreatedAt   time.Time
}

// Assessment is one judgement on an item: an optional score from 0 to 1 and
// an optional note, by one assessor, for one tag or for the item as a whole.
type Assessment struct {
	ID           int64
	ItemID       int64
	AssessorID   int64
	AssessorName string
	TagID        int64    // 0 = the item as a whole (stored as NULL)
	Score        *float64 // nil = no score; 0 is a score
	Note         string   // "" = no note (stored as NULL)
	UpdatedAt    time.Time
}

// ErrAssessorRequired is returned by ListItems when a minimum score or the
// score sort is requested without saying whose scores.
var ErrAssessorRequired = errors.New("min_score and sort \"score\" need an assessor")

// ── Assessors ───────────────────────────────────────────────

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func scanAssessor(r rowScanner) (*Assessor, error) {
	a := &Assessor{}
	var desc, color sql.NullString
	if err := r.Scan(&a.ID, &a.Name, &desc, &color, &a.CreatedAt); err != nil {
		return nil, err
	}
	a.Description = desc.String
	a.Color = color.String
	return a, nil
}

const assessorColumns = `id, name, description, color, created_at`

// InsertAssessor creates an assessor and returns its ID. A duplicate name is
// a UNIQUE constraint error.
func InsertAssessor(db *sql.DB, a *Assessor) (int64, error) {
	res, err := db.Exec(`INSERT INTO assessors (name, description, color) VALUES (?, ?, ?)`,
		a.Name, nullableString(a.Description), nullableString(a.Color))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetAssessor returns one assessor, or nil when it does not exist.
func GetAssessor(db *sql.DB, id int64) (*Assessor, error) {
	a, err := scanAssessor(db.QueryRow(`SELECT `+assessorColumns+` FROM assessors WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// GetAssessorByName returns the assessor with that exact name, or nil.
func GetAssessorByName(db *sql.DB, name string) (*Assessor, error) {
	a, err := scanAssessor(db.QueryRow(`SELECT `+assessorColumns+` FROM assessors WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// ListAssessors returns every assessor ordered by name.
func ListAssessors(db *sql.DB) ([]*Assessor, error) {
	rows, err := db.Query(`SELECT ` + assessorColumns + ` FROM assessors ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Assessor
	for rows.Next() {
		a, err := scanAssessor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountAssessors returns how many assessors exist.
func CountAssessors(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM assessors`).Scan(&n)
	return n, err
}

// UpdateAssessor writes an assessor's name, description and color. It
// reports whether an assessor with that ID existed.
func UpdateAssessor(db *sql.DB, a *Assessor) (bool, error) {
	res, err := db.Exec(`UPDATE assessors SET name = ?, description = ?, color = ? WHERE id = ?`,
		a.Name, nullableString(a.Description), nullableString(a.Color), a.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteAssessor removes an assessor and all of its assessments. Saved views
// that use it keep existing and stop filtering on it (see migration 8). It
// reports whether the assessor existed.
func DeleteAssessor(db *sql.DB, id int64) (bool, error) {
	return deleteByID(db, `DELETE FROM assessors WHERE id = ?`, id)
}

// ── Assessments ─────────────────────────────────────────────

const assessmentColumns = `a.id, a.item_id, a.assessor_id, r.name, a.tag_id, a.score, a.note, a.updated_at`

func scanAssessment(r rowScanner) (*Assessment, error) {
	a := &Assessment{}
	var tag sql.NullInt64
	var score sql.NullFloat64
	var note sql.NullString
	if err := r.Scan(&a.ID, &a.ItemID, &a.AssessorID, &a.AssessorName, &tag, &score, &note, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.TagID = tag.Int64
	if score.Valid {
		a.Score = &score.Float64
	}
	a.Note = note.String
	return a, nil
}

// PutAssessment stores an assessment, replacing the one with the same
// (item, assessor, tag) key, and returns what is stored. The item, assessor
// and tag (when TagID is not 0) must exist, else the foreign key fails. At
// least a score or a note is required (a CHECK). UpdatedAt is set here, in UTC
// with whole seconds, whatever the caller passes.
func PutAssessment(db *sql.DB, in *Assessment) (*Assessment, error) {
	now := time.Now().UTC().Truncate(time.Second)
	_, err := db.Exec(`INSERT INTO assessments (item_id, assessor_id, tag_id, score, note, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (item_id, assessor_id, IFNULL(tag_id, 0)) DO UPDATE
		SET score = excluded.score, note = excluded.note, updated_at = excluded.updated_at`,
		in.ItemID, in.AssessorID, zeroToNull(in.TagID), in.Score, nullableString(in.Note), now)
	if err != nil {
		return nil, err
	}
	return GetAssessment(db, in.ItemID, in.AssessorID, in.TagID)
}

func zeroToNull(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// GetAssessment returns the assessment with that key, or nil.
func GetAssessment(db *sql.DB, itemID, assessorID, tagID int64) (*Assessment, error) {
	a, err := scanAssessment(db.QueryRow(`SELECT `+assessmentColumns+`
		FROM assessments a JOIN assessors r ON r.id = a.assessor_id
		WHERE a.item_id = ? AND a.assessor_id = ? AND IFNULL(a.tag_id, 0) = ?`,
		itemID, assessorID, tagID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// DeleteAssessment removes the assessment with that key. It reports whether
// one existed.
func DeleteAssessment(db *sql.DB, itemID, assessorID, tagID int64) (bool, error) {
	res, err := db.Exec(`DELETE FROM assessments
		WHERE item_id = ? AND assessor_id = ? AND IFNULL(tag_id, 0) = ?`, itemID, assessorID, tagID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// loadAssessmentsByItemIDs loads the assessments of several items in one
// query, ordered by assessor name, then whole-item first, then tag.
func loadAssessmentsByItemIDs(db *sql.DB, itemIDs []int64) (map[int64][]*Assessment, error) {
	placeholders := make([]string, len(itemIDs))
	args := make([]interface{}, len(itemIDs))
	for i, id := range itemIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := db.Query(`SELECT `+assessmentColumns+`
		FROM assessments a JOIN assessors r ON r.id = a.assessor_id
		WHERE a.item_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY r.name, IFNULL(a.tag_id, 0)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64][]*Assessment)
	for rows.Next() {
		a, err := scanAssessment(rows)
		if err != nil {
			return nil, err
		}
		result[a.ItemID] = append(result[a.ItemID], a)
	}
	return result, rows.Err()
}

// assessmentScopeSQL returns the extra condition that keeps an assessment
// (alias a) in scope for the filter's tag, and its argument. See the filter
// semantics in docs/assessments-plan.md: with no tag every assessment is in
// scope; with a tag, whole-item assessments and those on the tag's subtree
// (or on the tag alone with TagExact) are.
func assessmentScopeSQL(f ItemFilter) (string, []any) {
	if f.TagID <= 0 {
		return "", nil
	}
	if f.TagExact {
		return ` AND (a.tag_id IS NULL OR a.tag_id = ?)`, []any{f.TagID}
	}
	return ` AND (a.tag_id IS NULL OR a.tag_id IN (` + SubtreeSQL + `))`, []any{f.TagID}
}

// validateAssessmentFilter checks the combination of fields.
func validateAssessmentFilter(f ItemFilter) error {
	if (f.MinScore != nil || f.Sort == "score") && f.AssessorID <= 0 {
		return ErrAssessorRequired
	}
	return nil
}
