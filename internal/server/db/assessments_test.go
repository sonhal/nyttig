package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fptr(f float64) *float64 { return &f }

func mustAssessor(t *testing.T, database *sql.DB, name string) int64 {
	t.Helper()
	id, err := InsertAssessor(database, &Assessor{Name: name})
	if err != nil {
		t.Fatalf("InsertAssessor(%q): %v", name, err)
	}
	return id
}

func mustItem(t *testing.T, database *sql.DB, src int64, title string, tagIDs ...int64) int64 {
	t.Helper()
	id, _, err := InsertItem(database, &Item{SourceID: src, GUID: title, Link: "http://example.com/" + title, Title: title})
	if err != nil {
		t.Fatalf("InsertItem(%q): %v", title, err)
	}
	for _, tag := range tagIDs {
		if err := AssignTagToItem(database, id, tag); err != nil {
			t.Fatalf("AssignTagToItem: %v", err)
		}
	}
	return id
}

func mustPut(t *testing.T, database *sql.DB, item, assessor, tag int64, score *float64, note string) *Assessment {
	t.Helper()
	a, err := PutAssessment(database, &Assessment{ItemID: item, AssessorID: assessor, TagID: tag, Score: score, Note: note})
	if err != nil {
		t.Fatalf("PutAssessment: %v", err)
	}
	return a
}

func countRows(t *testing.T, database *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAssessors_CRUD(t *testing.T) {
	database := openTestDB(t)
	id, err := InsertAssessor(database, &Assessor{Name: "claude", Description: "importance, 0-1", Color: "#112233"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetAssessor(database, id)
	if err != nil || got == nil {
		t.Fatalf("GetAssessor = %v, %v", got, err)
	}
	if got.Name != "claude" || got.Description != "importance, 0-1" || got.Color != "#112233" || got.CreatedAt.IsZero() {
		t.Fatalf("got %+v", got)
	}
	if byName, _ := GetAssessorByName(database, "claude"); byName == nil || byName.ID != id {
		t.Fatalf("GetAssessorByName = %+v", byName)
	}
	if a, err := GetAssessor(database, 999); a != nil || err != nil {
		t.Fatalf("GetAssessor(missing) = %v, %v", a, err)
	}

	if _, err := InsertAssessor(database, &Assessor{Name: "claude"}); err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("duplicate name error = %v", err)
	}

	// Empty description and color are stored as NULL.
	bare := mustAssessor(t, database, "cvss")
	var d, c sql.NullString
	if err := database.QueryRow(`SELECT description, color FROM assessors WHERE id = ?`, bare).Scan(&d, &c); err != nil {
		t.Fatal(err)
	}
	if d.Valid || c.Valid {
		t.Errorf("empty fields stored as %v, %v; want NULL", d, c)
	}

	if names := func() []string {
		list, _ := ListAssessors(database)
		var out []string
		for _, a := range list {
			out = append(out, a.Name)
		}
		return out
	}(); !reflect.DeepEqual(names, []string{"claude", "cvss"}) {
		t.Errorf("ListAssessors = %v", names)
	}

	got.Name, got.Description, got.Color = "claude-2", "", ""
	if ok, err := UpdateAssessor(database, got); err != nil || !ok {
		t.Fatalf("UpdateAssessor = %v, %v", ok, err)
	}
	if ok, _ := UpdateAssessor(database, &Assessor{ID: 999, Name: "x"}); ok {
		t.Error("UpdateAssessor of a missing assessor reported true")
	}
	if ok, err := DeleteAssessor(database, id); err != nil || !ok {
		t.Fatalf("DeleteAssessor = %v, %v", ok, err)
	}
	if ok, err := DeleteAssessor(database, id); err != nil || ok {
		t.Fatalf("second DeleteAssessor = %v, %v; want false, nil", ok, err)
	}
}

// The unique key treats a NULL tag as one value, and the upsert's conflict
// target accepts the IFNULL expression.
func TestPutAssessment_UpsertKey(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	cve := mustTag(t, database, "CVE")
	linux := mustTag(t, database, "linux")
	claude := mustAssessor(t, database, "claude")
	cvss := mustAssessor(t, database, "cvss")
	item := mustItem(t, database, src, "a", cve, linux)

	first := mustPut(t, database, item, claude, 0, fptr(0.4), "first")
	second := mustPut(t, database, item, claude, 0, fptr(0.9), "second")
	if first.ID != second.ID {
		t.Errorf("a whole-item re-assessment got a new row: %d then %d", first.ID, second.ID)
	}
	if second.Score == nil || *second.Score != 0.9 || second.Note != "second" || second.TagID != 0 || second.AssessorName != "claude" {
		t.Fatalf("after upsert: %+v", second)
	}
	if n := countRows(t, database, "assessments"); n != 1 {
		t.Fatalf("%d rows after two puts with the same key, want 1", n)
	}

	// A different tag, another assessor: separate rows. The same tag twice
	// replaces.
	mustPut(t, database, item, claude, cve, fptr(0.7), "")
	mustPut(t, database, item, claude, cve, fptr(0.8), "")
	mustPut(t, database, item, claude, linux, fptr(0.2), "")
	mustPut(t, database, item, cvss, 0, fptr(0.98), "CVE-2026-1234, CVSS 9.8")
	if n := countRows(t, database, "assessments"); n != 4 {
		t.Fatalf("%d rows, want 4", n)
	}

	// A note-only assessment has a NULL score, and replaces a scored one.
	noteOnly := mustPut(t, database, item, claude, cve, nil, "only a note")
	if noteOnly.Score != nil || noteOnly.Note != "only a note" {
		t.Fatalf("note-only: %+v", noteOnly)
	}
	// A score of 0 is a score, not "no score".
	zero := mustPut(t, database, item, claude, linux, fptr(0), "")
	if zero.Score == nil || *zero.Score != 0 {
		t.Fatalf("zero score: %+v", zero.Score)
	}

	// updated_at is UTC with whole seconds.
	var stored string
	if err := database.QueryRow(`SELECT updated_at || '' FROM assessments WHERE id = ?`, zero.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse("2006-01-02 15:04:05-07:00", stored); err != nil || !strings.HasSuffix(stored, "+00:00") {
		t.Errorf("updated_at stored as %q, want YYYY-MM-DD HH:MM:SS+00:00", stored)
	}

	if ok, err := DeleteAssessment(database, item, claude, 0); err != nil || !ok {
		t.Fatalf("DeleteAssessment = %v, %v", ok, err)
	}
	if ok, _ := DeleteAssessment(database, item, claude, 0); ok {
		t.Error("deleting twice reported true")
	}
	if a, _ := GetAssessment(database, item, claude, 0); a != nil {
		t.Errorf("deleted assessment still there: %+v", a)
	}
	if a, _ := GetAssessment(database, item, claude, cve); a == nil {
		t.Error("deleting the whole-item assessment removed the tag-scoped one")
	}
}

func TestAssessments_Checks(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	claude := mustAssessor(t, database, "claude")
	item := mustItem(t, database, src, "a")
	tag := mustTag(t, database, "x")

	for name, a := range map[string]*Assessment{
		"score above 1":     {ItemID: item, AssessorID: claude, Score: fptr(1.01)},
		"negative score":    {ItemID: item, AssessorID: claude, Score: fptr(-0.01)},
		"neither":           {ItemID: item, AssessorID: claude},
		"unknown item":      {ItemID: 999, AssessorID: claude, Score: fptr(0.5)},
		"unknown assessor":  {ItemID: item, AssessorID: 999, Score: fptr(0.5)},
		"unknown tag":       {ItemID: item, AssessorID: claude, TagID: tag + 100, Score: fptr(0.5)},
		"unknown tag, note": {ItemID: item, AssessorID: claude, TagID: tag + 100, Note: "n"},
	} {
		if _, err := PutAssessment(database, a); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Both ends are valid.
	mustPut(t, database, item, claude, 0, fptr(0), "")
	mustPut(t, database, item, claude, tag, fptr(1), "")
	if n := countRows(t, database, "assessments"); n != 2 {
		t.Errorf("%d rows, want 2", n)
	}
}

func TestAssessments_Cascades(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	tag := mustTag(t, database, "CVE")
	claude := mustAssessor(t, database, "claude")
	cvss := mustAssessor(t, database, "cvss")
	a := mustItem(t, database, src, "a", tag)
	b := mustItem(t, database, src, "b", tag)

	mustPut(t, database, a, claude, 0, fptr(0.1), "")
	mustPut(t, database, a, claude, tag, fptr(0.2), "")
	mustPut(t, database, b, claude, 0, fptr(0.3), "")
	mustPut(t, database, b, cvss, 0, fptr(0.4), "")

	// A tag takes only the assessments scoped to it.
	if ok, err := DeleteTag(database, tag); err != nil || !ok {
		t.Fatalf("DeleteTag = %v, %v", ok, err)
	}
	if n := countRows(t, database, "assessments"); n != 3 {
		t.Errorf("after tag delete: %d assessments, want 3 (whole-item ones stay)", n)
	}

	// An item takes all of its own.
	if _, err := database.Exec(`DELETE FROM items WHERE id = ?`, a); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, database, "assessments"); n != 2 {
		t.Errorf("after item delete: %d assessments, want 2", n)
	}

	// An assessor takes all of its own.
	if ok, err := DeleteAssessor(database, claude); err != nil || !ok {
		t.Fatalf("DeleteAssessor = %v, %v", ok, err)
	}
	if n := countRows(t, database, "assessments"); n != 1 {
		t.Errorf("after assessor delete: %d assessments, want 1", n)
	}
	// A source delete cascades through its items.
	if _, err := DeleteSource(database, src); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, database, "assessments"); n != 0 {
		t.Errorf("after source delete: %d assessments, want 0", n)
	}
}

func TestItems_CarryAssessments(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	tag := mustTag(t, database, "CVE")
	zed := mustAssessor(t, database, "zed")
	claude := mustAssessor(t, database, "claude")
	a := mustItem(t, database, src, "a")
	mustItem(t, database, src, "b")
	mustPut(t, database, a, zed, 0, fptr(0.5), "")
	mustPut(t, database, a, claude, tag, nil, "note")
	mustPut(t, database, a, claude, 0, fptr(0.1), "")

	check := func(name string, it *Item) {
		t.Helper()
		var got []string
		for _, as := range it.Assessments {
			got = append(got, as.AssessorName+"/"+string(rune('0'+as.TagID)))
		}
		// Ordered by assessor name, whole item before tags.
		if len(it.Assessments) != 3 || it.Assessments[0].AssessorName != "claude" || it.Assessments[0].TagID != 0 ||
			it.Assessments[1].TagID != tag || it.Assessments[2].AssessorName != "zed" {
			t.Errorf("%s: assessments %v", name, got)
		}
	}
	got, err := GetItem(database, a)
	if err != nil {
		t.Fatal(err)
	}
	check("GetItem", got)
	items, _, err := ListItems(database, ItemFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		switch it.Title {
		case "a":
			check("ListItems", it)
		case "b":
			if len(it.Assessments) != 0 {
				t.Errorf("b has assessments %v", it.Assessments)
			}
		}
	}
}

// assessFixture is the tag tree and assessments the filter tests share:
//
//	CVE ── critical          linux (unrelated)
//
//	A [CVE]            claude: whole item 0.5
//	B [critical]       claude: critical 0.9
//	C [CVE, linux]     claude: linux 0.95, CVE 0.3
//	D [CVE]            nothing
//	E [critical]       claude: CVE 0.8
//	F [linux]          claude: whole item 0.99 (never tagged CVE)
type assessFixture struct {
	database                   *sql.DB
	claude, cvss               int64
	cve, critical, linux       int64
	itemA, itemB, itemC, itemD int64
}

func newAssessFixture(t *testing.T) *assessFixture {
	t.Helper()
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	f := &assessFixture{database: database}
	f.cve = mustTag(t, database, "CVE")
	f.critical = mustTag(t, database, "critical")
	f.linux = mustTag(t, database, "linux")
	mustParents(t, database, f.critical, f.cve)
	f.claude = mustAssessor(t, database, "claude")
	f.cvss = mustAssessor(t, database, "cvss")

	// Distinct, descending publish times so the default order is A..F.
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	add := func(title string, hoursAgo int, tags ...int64) int64 {
		id, _, err := InsertItem(database, &Item{SourceID: src, GUID: title, Link: "http://x/" + title, Title: title,
			Published: ptrTime(base.Add(-time.Duration(hoursAgo) * time.Hour))})
		if err != nil {
			t.Fatal(err)
		}
		for _, tag := range tags {
			if err := AssignTagToItem(database, id, tag); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	a := add("A", 0, f.cve)
	b := add("B", 1, f.critical)
	c := add("C", 2, f.cve, f.linux)
	d := add("D", 3, f.cve)
	e := add("E", 4, f.critical)
	ff := add("F", 5, f.linux)
	f.itemA, f.itemB, f.itemC, f.itemD = a, b, c, d

	mustPut(t, database, a, f.claude, 0, fptr(0.5), "")
	mustPut(t, database, b, f.claude, f.critical, fptr(0.9), "")
	mustPut(t, database, c, f.claude, f.linux, fptr(0.95), "")
	mustPut(t, database, c, f.claude, f.cve, fptr(0.3), "")
	mustPut(t, database, e, f.claude, f.cve, fptr(0.8), "")
	mustPut(t, database, ff, f.claude, 0, fptr(0.99), "")
	// Another assessor with opposite opinions: never mixed in.
	mustPut(t, database, d, f.cvss, 0, fptr(1), "")
	mustPut(t, database, a, f.cvss, 0, fptr(0.1), "")
	return f
}

func ptrTime(t time.Time) *time.Time { return &t }

func (f *assessFixture) titles(t *testing.T, flt ItemFilter) []string {
	t.Helper()
	items, total, err := ListItems(f.database, flt)
	if err != nil {
		t.Fatalf("ListItems(%+v): %v", flt, err)
	}
	got := titles(items)
	if total != len(got) {
		t.Errorf("total = %d, but %d items returned", total, len(got))
	}
	return got
}

func TestListItems_MinScoreScope(t *testing.T) {
	f := newAssessFixture(t)
	for _, tc := range []struct {
		name string
		flt  ItemFilter
		want []string
	}{
		// No tag: every assessment counts, whatever its tag.
		{"no tag", ItemFilter{AssessorID: f.claude, MinScore: fptr(0.7)}, []string{"B", "C", "E", "F"}},
		// Parent tag: B's score is on a child (in the subtree), E's on the
		// parent itself, C's 0.95 is on an unrelated tag and doesn't count,
		// and F isn't tagged CVE.
		{"parent tag", ItemFilter{TagID: f.cve, AssessorID: f.claude, MinScore: fptr(0.7)}, []string{"B", "E"}},
		// Exact: items tagged CVE itself, and only CVE-scoped or whole-item
		// scores. C's CVE score is 0.3.
		{"parent tag exact", ItemFilter{TagID: f.cve, TagExact: true, AssessorID: f.claude, MinScore: fptr(0.4)}, []string{"A"}},
		{"parent tag exact, low bar", ItemFilter{TagID: f.cve, TagExact: true, AssessorID: f.claude, MinScore: fptr(0.3)}, []string{"A", "C"}},
		// Child tag: E's score is on the parent, outside the child's subtree.
		{"child tag", ItemFilter{TagID: f.critical, AssessorID: f.claude, MinScore: fptr(0.7)}, []string{"B"}},
		// Whole-item scores are in scope for any tag.
		{"whole item in scope", ItemFilter{TagID: f.cve, AssessorID: f.claude, MinScore: fptr(0.4)}, []string{"A", "B", "E"}},
		{"unrelated tag filter", ItemFilter{TagID: f.linux, AssessorID: f.claude, MinScore: fptr(0.9)}, []string{"C", "F"}},
		// The bound is inclusive, and another assessor's scores don't count.
		{"inclusive", ItemFilter{AssessorID: f.claude, MinScore: fptr(0.9)}, []string{"B", "C", "F"}},
		{"zero matches scored items only", ItemFilter{AssessorID: f.cvss, MinScore: fptr(0)}, []string{"A", "D"}},
		{"other assessor", ItemFilter{AssessorID: f.cvss, MinScore: fptr(0.9)}, []string{"D"}},
	} {
		if got := f.titles(t, tc.flt); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestListItems_UnassessedBy(t *testing.T) {
	f := newAssessFixture(t)
	for _, tc := range []struct {
		name string
		flt  ItemFilter
		want []string
	}{
		{"no tag", ItemFilter{UnassessedBy: f.claude}, []string{"D"}},
		// Under CVE: A (whole), B, C (CVE 0.3), E (CVE) are assessed; D isn't.
		{"parent tag", ItemFilter{TagID: f.cve, UnassessedBy: f.claude}, []string{"D"}},
		// Under critical, E's assessment is on the parent: out of scope.
		{"child tag", ItemFilter{TagID: f.critical, UnassessedBy: f.claude}, []string{"E"}},
		{"exact", ItemFilter{TagID: f.cve, TagExact: true, UnassessedBy: f.claude}, []string{"D"}},
		// C's only linux-scoped score doesn't make it assessed for CVE, but
		// it has a CVE one too; under linux it is assessed.
		{"linux", ItemFilter{TagID: f.linux, UnassessedBy: f.cvss}, []string{"C", "F"}},
		{"other assessor", ItemFilter{UnassessedBy: f.cvss}, []string{"B", "C", "E", "F"}},
		// Combines with the assessor's own minimum score.
		{"combined", ItemFilter{TagID: f.cve, AssessorID: f.cvss, MinScore: fptr(0.5), UnassessedBy: f.claude}, []string{"D"}},
	} {
		if got := f.titles(t, tc.flt); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestListItems_SortScore(t *testing.T) {
	f := newAssessFixture(t)
	for _, tc := range []struct {
		name string
		flt  ItemFilter
		want []string
	}{
		// C's 0.95 is on linux and doesn't count under CVE (its CVE score
		// is 0.3). D has none and goes last, with no ties to break.
		{"under CVE", ItemFilter{TagID: f.cve, AssessorID: f.claude, Sort: "score"}, []string{"B", "E", "A", "C", "D"}},
		{"exact", ItemFilter{TagID: f.cve, TagExact: true, AssessorID: f.claude, Sort: "score"}, []string{"A", "C", "D"}},
		{"no tag takes the highest", ItemFilter{AssessorID: f.claude, Sort: "score"}, []string{"F", "C", "B", "E", "A", "D"}},
		// Unscored items keep newest-first among themselves.
		{"other assessor", ItemFilter{AssessorID: f.cvss, Sort: "score"}, []string{"D", "A", "B", "C", "E", "F"}},
		{"with a minimum", ItemFilter{TagID: f.cve, AssessorID: f.claude, MinScore: fptr(0.4), Sort: "score"}, []string{"B", "E", "A"}},
		// assessor_id alone keeps the default order.
		{"assessor alone", ItemFilter{AssessorID: f.claude}, []string{"A", "B", "C", "D", "E", "F"}},
	} {
		if got := f.titles(t, tc.flt); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	// Paging keeps the order.
	page, _, err := ListItems(f.database, ItemFilter{TagID: f.cve, AssessorID: f.claude, Sort: "score", Limit: 2, Offset: 2})
	if err != nil || !reflect.DeepEqual(titles(page), []string{"A", "C"}) {
		t.Errorf("page = %v, %v", titles(page), err)
	}

	// A note-only assessment is not a score: the item sorts as unscored.
	mustPut(t, f.database, f.itemD, f.claude, 0, nil, "just a note")
	got := f.titles(t, ItemFilter{TagID: f.cve, AssessorID: f.claude, Sort: "score"})
	if !reflect.DeepEqual(got, []string{"B", "E", "A", "C", "D"}) {
		t.Errorf("note-only item: %v", got)
	}
	// It does count as assessed.
	if got := f.titles(t, ItemFilter{TagID: f.cve, UnassessedBy: f.claude}); len(got) != 0 {
		t.Errorf("unassessed after a note: %v", got)
	}
}

func TestListItems_AssessmentFilterNeedsAssessor(t *testing.T) {
	f := newAssessFixture(t)
	for name, flt := range map[string]ItemFilter{
		"min score":  {MinScore: fptr(0.5)},
		"sort score": {Sort: "score"},
	} {
		if _, _, err := ListItems(f.database, flt); !errors.Is(err, ErrAssessorRequired) {
			t.Errorf("%s: err = %v, want ErrAssessorRequired", name, err)
		}
	}
}

// migrateTo applies the embedded migrations from..to (inclusive) to a bare
// database, the way runMigrations does (one transaction each, foreign keys on).
func migrateTo(t *testing.T, database *sql.DB, from, to int) {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for v := from; v <= to; v++ {
		prefix := ""
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".up.sql") && strings.HasPrefix(e.Name(), pad6(v)+"_") {
				prefix = e.Name()
			}
		}
		if prefix == "" {
			t.Fatalf("no migration %d", v)
		}
		body, _ := migrationsFS.ReadFile("migrations/" + prefix)
		tx, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("migration %d: %v", v, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func pad6(v int) string {
	s := "000000" + itoa(v)
	return s[len(s)-6:]
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for ; v > 0; v /= 10 {
		b = append([]byte{byte('0' + v%10)}, b...)
	}
	return string(b)
}

func TestMigration7_RebuildKeepsSavedViews(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	migrateTo(t, database, 1, 6)

	if _, err := database.Exec(`INSERT INTO sources (id, name, url, type, refresh_sec, enabled) VALUES (3, 's', 'http://x', 'rss', 60, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO tags (id, name) VALUES (4, 't')`); err != nil {
		t.Fatal(err)
	}
	// Ids with gaps, mixed positions and favorites, a NOCASE name.
	rows := []string{
		`(7, 'Security', 'cve', 3, 4, 'oldest', 1, 1, 2, '2026-01-02 03:04:05')`,
		`(9, 'plain', '', NULL, NULL, 'newest', 0, 0, 0, '2026-02-03 04:05:06')`,
		`(12, 'Fav', 'x', NULL, 4, 'newest', 0, 1, 1, '2026-03-04 05:06:07')`,
	}
	for _, r := range rows {
		if _, err := database.Exec(`INSERT INTO saved_views (id, name, search, source_id, tag_id, sort, unviewed_only, favorite, position, created_at) VALUES ` + r); err != nil {
			t.Fatal(err)
		}
	}
	migrateTo(t, database, 7, 7)

	views, err := ListSavedViews(database)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 3 {
		t.Fatalf("%d views after migration, want 3", len(views))
	}
	// Display order is position, id.
	src, tag := int64(3), int64(4)
	want := []*SavedView{
		{ID: 9, Name: "plain", Sort: "newest", Position: 0},
		{ID: 12, Name: "Fav", Search: "x", TagID: &tag, Sort: "newest", Favorite: true, Position: 1},
		{ID: 7, Name: "Security", Search: "cve", SourceID: &src, TagID: &tag, Sort: "oldest", UnviewedOnly: true, Favorite: true, Position: 2},
	}
	if !reflect.DeepEqual(views, want) {
		t.Fatalf("views after migration:\n got %+v %+v %+v\nwant %+v %+v %+v", views[0], views[1], views[2], want[0], want[1], want[2])
	}
	var created string
	if err := database.QueryRow(`SELECT created_at FROM saved_views WHERE id = 7`).Scan(&created); err != nil || created != "2026-01-02 03:04:05" {
		t.Errorf("created_at = %q, %v", created, err)
	}

	// The rebuilt table still enforces NOCASE names, the new sort, the old
	// foreign keys and its indexes.
	if _, err := database.Exec(`INSERT INTO saved_views (name) VALUES ('SECURITY')`); err == nil {
		t.Error("duplicate name (case-insensitive) accepted after rebuild")
	}
	if _, err := database.Exec(`INSERT INTO saved_views (name, sort) VALUES ('s2', 'score')`); err != nil {
		t.Errorf("sort 'score' rejected: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO saved_views (name, sort) VALUES ('s3', 'random')`); err == nil {
		t.Error("sort 'random' accepted")
	}
	if _, err := database.Exec(`DELETE FROM tags WHERE id = 4`); err != nil {
		t.Fatal(err)
	}
	var tagID sql.NullInt64
	if err := database.QueryRow(`SELECT tag_id FROM saved_views WHERE id = 7`).Scan(&tagID); err != nil || tagID.Valid {
		t.Errorf("tag_id after tag delete = %v, %v; want NULL", tagID, err)
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'saved_views' AND name LIKE 'idx_saved_views_%'`).Scan(&n); err != nil || n != 4 {
		t.Errorf("%d saved_views indexes, want 4 (%v)", n, err)
	}
}

func TestSavedViews_AssessorFields(t *testing.T) {
	database := openTestDB(t)
	claude := mustAssessor(t, database, "claude")
	cvss := mustAssessor(t, database, "cvss")
	min := 0.7
	id := addView(t, database, &SavedView{Name: "important", AssessorID: &claude, MinScore: &min, Sort: "score", UnassessedBy: &cvss})

	got, _ := GetSavedView(database, id)
	if got.AssessorID == nil || *got.AssessorID != claude || got.MinScore == nil || *got.MinScore != 0.7 ||
		got.UnassessedBy == nil || *got.UnassessedBy != cvss || got.Sort != "score" {
		t.Fatalf("view = %+v", got)
	}
	if n, _ := CountViewsUsingAssessor(database, claude); n != 1 {
		t.Errorf("CountViewsUsingAssessor(claude) = %d, want 1", n)
	}
	if n, _ := CountViewsUsingAssessor(database, cvss); n != 1 {
		t.Errorf("CountViewsUsingAssessor(cvss) = %d, want 1 (unassessed_by counts)", n)
	}

	got.MinScore, got.UnassessedBy = nil, nil
	if ok, err := UpdateSavedView(database, got); err != nil || !ok {
		t.Fatalf("UpdateSavedView = %v, %v", ok, err)
	}
	again, _ := GetSavedView(database, id)
	if again.MinScore != nil || again.UnassessedBy != nil || again.AssessorID == nil {
		t.Errorf("after update: %+v", again)
	}
}

// Deleting an assessor keeps the views and drops the assessor fields; a
// "score" sort would have no assessor, so it goes back to "newest".
func TestSavedViews_DeleteAssessorKeepsView(t *testing.T) {
	database := openTestDB(t)
	claude := mustAssessor(t, database, "claude")
	cvss := mustAssessor(t, database, "cvss")
	min := 0.7
	tag := mustTag(t, database, "CVE")

	byScore := addView(t, database, &SavedView{Name: "by score", Search: "q", TagID: &tag, AssessorID: &claude, MinScore: &min, Sort: "score", Favorite: true})
	oldest := addView(t, database, &SavedView{Name: "oldest", AssessorID: &claude, Sort: "oldest"})
	work := addView(t, database, &SavedView{Name: "work", UnassessedBy: &claude})
	other := addView(t, database, &SavedView{Name: "other", AssessorID: &cvss, MinScore: &min, Sort: "score"})

	if ok, err := DeleteAssessor(database, claude); err != nil || !ok {
		t.Fatalf("DeleteAssessor = %v, %v", ok, err)
	}

	v, _ := GetSavedView(database, byScore)
	if v == nil || v.AssessorID != nil || v.MinScore != nil || v.Sort != "newest" || v.Search != "q" || v.TagID == nil || !v.Favorite {
		t.Errorf("score view after delete: %+v", v)
	}
	v, _ = GetSavedView(database, oldest)
	if v == nil || v.AssessorID != nil || v.Sort != "oldest" {
		t.Errorf("oldest view after delete: %+v", v)
	}
	v, _ = GetSavedView(database, work)
	if v == nil || v.UnassessedBy != nil {
		t.Errorf("unassessed view after delete: %+v", v)
	}
	// Another assessor's views are untouched.
	v, _ = GetSavedView(database, other)
	if v == nil || v.AssessorID == nil || *v.AssessorID != cvss || v.MinScore == nil || v.Sort != "score" {
		t.Errorf("other view changed: %+v", v)
	}
	if n, _ := CountViewsUsingAssessor(database, claude); n != 0 {
		t.Errorf("CountViewsUsingAssessor after delete = %d, want 0", n)
	}
}

// With TagExact an assessment scoped to a child tag is out of scope, even on
// an item that carries both tags.
func TestListItems_ExactScopeExcludesChildTags(t *testing.T) {
	f := newAssessFixture(t)
	src := testSource(t, f.database, "other")
	g := mustItem(t, f.database, src, "G", f.cve, f.critical)
	mustPut(t, f.database, g, f.claude, f.critical, fptr(0.6), "")

	if got := f.titles(t, ItemFilter{TagID: f.cve, AssessorID: f.claude, MinScore: fptr(0.55)}); !reflect.DeepEqual(got, []string{"B", "E", "G"}) {
		t.Errorf("subtree: %v", got)
	}
	if got := f.titles(t, ItemFilter{TagID: f.cve, TagExact: true, AssessorID: f.claude, MinScore: fptr(0.55)}); len(got) != 0 {
		t.Errorf("exact min score: %v, want none", got)
	}
	if got := f.titles(t, ItemFilter{TagID: f.cve, TagExact: true, UnassessedBy: f.claude}); !reflect.DeepEqual(got, []string{"D", "G"}) {
		t.Errorf("exact unassessed: %v, want [D G]", got)
	}
}
