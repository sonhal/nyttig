package db

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func mustSeries(t *testing.T, database *sql.DB, assessor int64, name string) int64 {
	t.Helper()
	id, err := InsertDigestSeries(database, &DigestSeries{AssessorID: assessor, Name: name})
	if err != nil {
		t.Fatalf("InsertDigestSeries(%q): %v", name, err)
	}
	return id
}

func day(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func mustDigest(t *testing.T, database *sql.DB, series int64, title string, end time.Time, items, inputs []int64) int64 {
	t.Helper()
	id, err := InsertDigest(database, &NewDigest{
		SeriesID: series, Title: title, Body: "body of " + title,
		PeriodStart: end.Add(-24 * time.Hour), PeriodEnd: end, ItemIDs: items, InputIDs: inputs,
	})
	if err != nil {
		t.Fatalf("InsertDigest(%q): %v", title, err)
	}
	return id
}

func digestTitles(ds []*Digest) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Title
	}
	return out
}

func TestDigestSeries_CRUD(t *testing.T) {
	database := openTestDB(t)
	claude := mustAssessor(t, database, "claude")
	gpt := mustAssessor(t, database, "gpt")

	id, err := InsertDigestSeries(database, &DigestSeries{AssessorID: claude, Name: "daily-cve", Description: "CVE news"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetDigestSeries(database, id)
	if err != nil || got == nil {
		t.Fatalf("GetDigestSeries = %v, %v", got, err)
	}
	if got.Name != "daily-cve" || got.Description != "CVE news" || got.AssessorName != "claude" ||
		got.DigestCount != 0 || got.LatestPeriodEnd != nil || got.CreatedAt.IsZero() {
		t.Fatalf("got %+v", got)
	}
	if s, err := GetDigestSeries(database, 999); s != nil || err != nil {
		t.Fatalf("GetDigestSeries(missing) = %v, %v", s, err)
	}

	// Unique per assessor, case-insensitive; another assessor may reuse it.
	if _, err := InsertDigestSeries(database, &DigestSeries{AssessorID: claude, Name: "DAILY-cve"}); err == nil ||
		!strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("duplicate name error = %v", err)
	}
	mustSeries(t, database, gpt, "daily-cve")

	// Unknown assessor fails the foreign key.
	if _, err := InsertDigestSeries(database, &DigestSeries{AssessorID: 999, Name: "x"}); err == nil {
		t.Fatal("unknown assessor accepted")
	}

	ok, err := UpdateDigestSeries(database, &DigestSeries{ID: id, Name: "cve", Description: ""})
	if err != nil || !ok {
		t.Fatalf("UpdateDigestSeries = %v, %v", ok, err)
	}
	got, _ = GetDigestSeries(database, id)
	if got.Name != "cve" || got.Description != "" {
		t.Fatalf("after update %+v", got)
	}
	if ok, _ := UpdateDigestSeries(database, &DigestSeries{ID: 999, Name: "n"}); ok {
		t.Fatal("update of missing series reported true")
	}

	if ok, err := DeleteDigestSeries(database, id); err != nil || !ok {
		t.Fatalf("DeleteDigestSeries = %v, %v", ok, err)
	}
	if ok, _ := DeleteDigestSeries(database, id); ok {
		t.Fatal("second delete reported true")
	}
}

func TestListDigestSeries_OrderCountsAndAssessorFilter(t *testing.T) {
	database := openTestDB(t)
	claude := mustAssessor(t, database, "claude")
	gpt := mustAssessor(t, database, "gpt")
	a := mustSeries(t, database, claude, "a")
	b := mustSeries(t, database, gpt, "b")
	c := mustSeries(t, database, claude, "c")
	mustDigest(t, database, a, "a1", day(3), nil, nil)
	mustDigest(t, database, a, "a2", day(5), nil, nil)
	mustDigest(t, database, a, "a0", day(1), nil, nil)

	all, err := ListDigestSeries(database, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != a || all[1].ID != b || all[2].ID != c {
		t.Fatalf("order = %+v", all)
	}
	if all[0].DigestCount != 3 || all[0].LatestPeriodEnd == nil || !all[0].LatestPeriodEnd.Equal(day(5)) {
		t.Fatalf("series a = %+v latest %v", all[0], all[0].LatestPeriodEnd)
	}
	if all[1].DigestCount != 0 || all[1].LatestPeriodEnd != nil {
		t.Fatalf("series b = %+v", all[1])
	}
	onlyClaude, _ := ListDigestSeries(database, claude)
	if len(onlyClaude) != 2 || onlyClaude[0].ID != a || onlyClaude[1].ID != c {
		t.Fatalf("claude's series = %+v", onlyClaude)
	}
}

func TestReorderDigestSeries(t *testing.T) {
	database := openTestDB(t)
	claude := mustAssessor(t, database, "claude")
	a := mustSeries(t, database, claude, "a")
	b := mustSeries(t, database, claude, "b")
	c := mustSeries(t, database, claude, "c")

	if err := ReorderDigestSeries(database, []int64{c, a, b}); err != nil {
		t.Fatal(err)
	}
	list, _ := ListDigestSeries(database, 0)
	if list[0].ID != c || list[1].ID != a || list[2].ID != b {
		t.Fatalf("order after reorder: %v %v %v", list[0].ID, list[1].ID, list[2].ID)
	}
	// A new series goes last.
	d := mustSeries(t, database, claude, "d")
	list, _ = ListDigestSeries(database, 0)
	if list[3].ID != d {
		t.Fatalf("new series not last: %+v", list)
	}

	for name, ids := range map[string][]int64{
		"missing":   {a, b, c},
		"duplicate": {a, a, b, c},
		"unknown":   {a, b, c, 999},
		"empty":     {},
	} {
		if err := ReorderDigestSeries(database, ids); !errors.Is(err, ErrDigestSeriesOrder) {
			t.Errorf("%s: err = %v, want ErrDigestSeriesOrder", name, err)
		}
	}
	list, _ = ListDigestSeries(database, 0)
	if list[0].ID != c {
		t.Fatalf("a failed reorder changed the order")
	}
}

func TestDigest_InsertGetWithLinks(t *testing.T) {
	database := openTestDB(t)
	claude := mustAssessor(t, database, "claude")
	series := mustSeries(t, database, claude, "daily")
	src := testSource(t, database, "feed")
	i1 := mustItem(t, database, src, "one")
	i2 := mustItem(t, database, src, "two")

	first := mustDigest(t, database, series, "first", day(4), []int64{i1}, nil)
	// Duplicates in the link lists are dropped.
	second := mustDigest(t, database, series, "second", day(5), []int64{i1, i2, i1}, []int64{first, first})

	d, err := GetDigest(database, second)
	if err != nil || d == nil {
		t.Fatalf("GetDigest = %v, %v", d, err)
	}
	if d.Title != "second" || d.Body != "body of second" || d.SeriesName != "daily" || d.AssessorName != "claude" ||
		d.SeriesID != series || d.AssessorID != claude {
		t.Fatalf("got %+v", d)
	}
	if !d.PeriodStart.Equal(day(4)) || !d.PeriodEnd.Equal(day(5)) || d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() {
		t.Fatalf("dates %v %v %v %v", d.PeriodStart, d.PeriodEnd, d.CreatedAt, d.UpdatedAt)
	}
	if len(d.Items) != 2 {
		t.Fatalf("items = %+v", d.Items)
	}
	titlesOf := map[string]bool{}
	for _, it := range d.Items {
		titlesOf[it.Title] = true
		if it.SourceName != "feed" || !strings.HasPrefix(it.Link, "http://example.com/") {
			t.Errorf("item %+v", it)
		}
	}
	if !titlesOf["one"] || !titlesOf["two"] {
		t.Fatalf("items = %+v", d.Items)
	}
	if len(d.Inputs) != 1 || d.Inputs[0].ID != first || d.Inputs[0].Title != "first" ||
		d.Inputs[0].SeriesName != "daily" || !d.Inputs[0].PeriodEnd.Equal(day(4)) {
		t.Fatalf("inputs = %+v", d.Inputs)
	}
	if g, err := GetDigest(database, 999); g != nil || err != nil {
		t.Fatalf("GetDigest(missing) = %v, %v", g, err)
	}

	// Unknown item or input fails and leaves nothing behind.
	before := countRows(t, database, "digests")
	if _, err := InsertDigest(database, &NewDigest{SeriesID: series, Title: "t", Body: "b", PeriodStart: day(1), PeriodEnd: day(1),
		ItemIDs: []int64{i1, 999}}); err == nil {
		t.Fatal("unknown item accepted")
	}
	if _, err := InsertDigest(database, &NewDigest{SeriesID: series, Title: "t", Body: "b", PeriodStart: day(1), PeriodEnd: day(1),
		InputIDs: []int64{999}}); err == nil {
		t.Fatal("unknown input accepted")
	}
	if n := countRows(t, database, "digests"); n != before {
		t.Fatalf("a failed insert left %d digests", n-before)
	}
}

func TestDigest_Checks(t *testing.T) {
	database := openTestDB(t)
	series := mustSeries(t, database, mustAssessor(t, database, "claude"), "daily")

	if _, err := InsertDigest(database, &NewDigest{SeriesID: series, Title: "t", Body: "b", PeriodStart: day(5), PeriodEnd: day(4)}); err == nil ||
		!strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("period_end before period_start: err = %v", err)
	}
	// Equal is fine.
	id := mustDigest(t, database, series, "ok", day(5), nil, nil)

	// Updating only one end can break the order too.
	early := day(1)
	if _, err := UpdateDigest(database, id, &DigestUpdate{PeriodEnd: &early}); err == nil {
		t.Fatal("update to period_end before period_start accepted")
	}
	// A digest is not its own input.
	if _, err := UpdateDigest(database, id, &DigestUpdate{InputIDs: &[]int64{id}}); err == nil ||
		!strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("self input: err = %v", err)
	}
	d, _ := GetDigest(database, id)
	if len(d.Inputs) != 0 || !d.PeriodEnd.Equal(day(5)) {
		t.Fatalf("a failed update changed the digest: %+v", d)
	}
}

func TestDigest_UpdateReplacesLinkSets(t *testing.T) {
	database := openTestDB(t)
	series := mustSeries(t, database, mustAssessor(t, database, "claude"), "daily")
	src := testSource(t, database, "feed")
	i1 := mustItem(t, database, src, "one")
	i2 := mustItem(t, database, src, "two")
	i3 := mustItem(t, database, src, "three")
	older := mustDigest(t, database, series, "older", day(3), nil, nil)
	other := mustDigest(t, database, series, "other", day(4), nil, nil)
	id := mustDigest(t, database, series, "d", day(5), []int64{i1, i2}, []int64{older})
	createdBefore, _ := GetDigest(database, id)

	// Unset sets are unchanged; set ones are replaced.
	title := "new title"
	ok, err := UpdateDigest(database, id, &DigestUpdate{Title: &title, ItemIDs: &[]int64{i3}})
	if err != nil || !ok {
		t.Fatalf("UpdateDigest = %v, %v", ok, err)
	}
	d, _ := GetDigest(database, id)
	if d.Title != "new title" || d.Body != "body of d" {
		t.Fatalf("fields %+v", d)
	}
	if len(d.Items) != 1 || d.Items[0].ItemID != i3 {
		t.Fatalf("items = %+v", d.Items)
	}
	if len(d.Inputs) != 1 || d.Inputs[0].ID != older {
		t.Fatalf("inputs changed: %+v", d.Inputs)
	}
	if !d.CreatedAt.Equal(createdBefore.CreatedAt) || d.UpdatedAt.Before(createdBefore.UpdatedAt) {
		t.Fatalf("created %v -> %v, updated %v -> %v", createdBefore.CreatedAt, d.CreatedAt, createdBefore.UpdatedAt, d.UpdatedAt)
	}

	// An empty set clears; a new input set replaces.
	if _, err := UpdateDigest(database, id, &DigestUpdate{ItemIDs: &[]int64{}, InputIDs: &[]int64{other}}); err != nil {
		t.Fatal(err)
	}
	d, _ = GetDigest(database, id)
	if len(d.Items) != 0 || len(d.Inputs) != 1 || d.Inputs[0].ID != other {
		t.Fatalf("after replace: items %+v inputs %+v", d.Items, d.Inputs)
	}

	body := "new body"
	end := day(6)
	if _, err := UpdateDigest(database, id, &DigestUpdate{Body: &body, PeriodEnd: &end}); err != nil {
		t.Fatal(err)
	}
	d, _ = GetDigest(database, id)
	if d.Body != "new body" || !d.PeriodEnd.Equal(day(6)) || !d.PeriodStart.Equal(day(4)) {
		t.Fatalf("after body/period update %+v", d)
	}

	if ok, err := UpdateDigest(database, 999, &DigestUpdate{Title: &title}); ok || err != nil {
		t.Fatalf("UpdateDigest(missing) = %v, %v", ok, err)
	}
}

func TestDigest_Cascades(t *testing.T) {
	database := openTestDB(t)
	claude := mustAssessor(t, database, "claude")
	gpt := mustAssessor(t, database, "gpt")
	sa := mustSeries(t, database, claude, "a")
	sb := mustSeries(t, database, gpt, "b")
	src := testSource(t, database, "feed")
	item := mustItem(t, database, src, "one")
	keep := mustDigest(t, database, sb, "keep", day(4), nil, nil)
	d1 := mustDigest(t, database, sa, "d1", day(5), []int64{item}, []int64{keep})
	d2 := mustDigest(t, database, sa, "d2", day(6), nil, []int64{d1})

	// Deleting an item (with its source) keeps the digest, drops the link.
	if _, err := DeleteSource(database, src); err != nil {
		t.Fatal(err)
	}
	d, _ := GetDigest(database, d1)
	if d == nil || len(d.Items) != 0 || len(d.Inputs) != 1 {
		t.Fatalf("after item delete: %+v", d)
	}
	if n := countRows(t, database, "digest_items"); n != 0 {
		t.Fatalf("%d digest_items left", n)
	}

	// Deleting a digest removes it from other digests' inputs, not them.
	if ok, err := DeleteDigest(database, d1); err != nil || !ok {
		t.Fatalf("DeleteDigest = %v, %v", ok, err)
	}
	if ok, _ := DeleteDigest(database, d1); ok {
		t.Fatal("second delete reported true")
	}
	d, _ = GetDigest(database, d2)
	if d == nil || len(d.Inputs) != 0 {
		t.Fatalf("d2 after input delete: %+v", d)
	}
	if k, _ := GetDigest(database, keep); k == nil {
		t.Fatal("an unrelated digest was deleted")
	}

	// Series delete takes its digests; assessor delete takes series and
	// digests, links included.
	mustDigest(t, database, sa, "d3", day(7), nil, []int64{keep})
	if n, _ := CountDigestsByAssessor(database, claude); n != 2 {
		t.Fatalf("CountDigestsByAssessor(claude) = %d, want 2", n)
	}
	if ok, err := DeleteDigestSeries(database, sa); err != nil || !ok {
		t.Fatalf("DeleteDigestSeries = %v, %v", ok, err)
	}
	if n, _ := CountDigestsByAssessor(database, claude); n != 0 {
		t.Fatalf("digests left after series delete: %d", n)
	}
	if ok, err := DeleteAssessor(database, gpt); err != nil || !ok {
		t.Fatalf("DeleteAssessor = %v, %v", ok, err)
	}
	for _, table := range []string{"digest_series", "digests", "digest_items", "digest_inputs"} {
		if n := countRows(t, database, table); n != 0 {
			t.Errorf("%s has %d rows after deleting every assessor's data", table, n)
		}
	}
}

func TestListDigests_OrderAndCursor(t *testing.T) {
	database := openTestDB(t)
	claude := mustAssessor(t, database, "claude")
	series := mustSeries(t, database, claude, "daily")
	other := mustSeries(t, database, claude, "other")
	mustDigest(t, database, other, "elsewhere", day(9), nil, nil)

	// Three share a period_end; the id breaks the tie, newest id first.
	mustDigest(t, database, series, "old", day(1), nil, nil)
	mustDigest(t, database, series, "tie-a", day(3), nil, nil)
	mustDigest(t, database, series, "tie-b", day(3), nil, nil)
	mustDigest(t, database, series, "tie-c", day(3), nil, nil)
	mustDigest(t, database, series, "new", day(5), nil, nil)

	want := []string{"new", "tie-c", "tie-b", "tie-a", "old"}
	all, more, err := ListDigests(database, series, 0, 0, false)
	if err != nil || more {
		t.Fatalf("ListDigests = %v, more %v, %v", digestTitles(all), more, err)
	}
	if !reflect.DeepEqual(digestTitles(all), want) {
		t.Fatalf("order = %v, want %v", digestTitles(all), want)
	}

	// Page through with limit 2 and the last ID as the cursor.
	var got []string
	var cursor int64
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("paging does not end")
		}
		page, more, err := ListDigests(database, series, cursor, 2, false)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, digestTitles(page)...)
		if !more {
			break
		}
		cursor = page[len(page)-1].ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paged = %v, want %v", got, want)
	}

	// A cursor in another series or unknown gives an empty page.
	if page, more, err := ListDigests(database, series, 999, 2, false); err != nil || len(page) != 0 || more {
		t.Fatalf("unknown cursor = %v, %v, %v", digestTitles(page), more, err)
	}
	// The limit is capped at 100.
	for i := 0; i < 105; i++ {
		mustDigest(t, database, other, "bulk", day(2), nil, nil)
	}
	page, more, _ := ListDigests(database, other, 0, 1000, false)
	if len(page) != 100 || !more {
		t.Fatalf("capped page has %d, more %v", len(page), more)
	}
}

func TestListDigests_WithBodyAndLinks(t *testing.T) {
	database := openTestDB(t)
	series := mustSeries(t, database, mustAssessor(t, database, "claude"), "daily")
	src := testSource(t, database, "feed")
	item := mustItem(t, database, src, "one")
	first := mustDigest(t, database, series, "first", day(4), []int64{item}, nil)
	mustDigest(t, database, series, "second", day(5), nil, []int64{first})

	list, _, err := ListDigests(database, series, 0, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Body != "" || list[1].Body != "" {
		t.Fatalf("bodies in a list without withBody: %q %q", list[0].Body, list[1].Body)
	}
	if len(list[0].Inputs) != 1 || len(list[1].Items) != 1 {
		t.Fatalf("links not filled: %+v %+v", list[0], list[1])
	}
	list, _, _ = ListDigests(database, series, 0, 10, true)
	if list[0].Body != "body of second" || list[1].Body != "body of first" {
		t.Fatalf("bodies = %q %q", list[0].Body, list[1].Body)
	}
}

// Digest dates have the text shape of items.published, so ordering by text is
// ordering by time.
func TestDigest_DateShapeMatchesItems(t *testing.T) {
	database := openTestDB(t)
	series := mustSeries(t, database, mustAssessor(t, database, "claude"), "daily")
	src := testSource(t, database, "feed")

	// A non-UTC time with fractions is normalized on the way in.
	zone := time.FixedZone("x", 2*3600)
	at := time.Date(2026, 10, 5, 12, 30, 45, 987654321, zone)
	pub := at
	if _, _, err := InsertItem(database, &Item{SourceID: src, GUID: "g", Link: "http://example.com/g", Title: "g", Published: &pub}); err != nil {
		t.Fatal(err)
	}
	id, err := InsertDigest(database, &NewDigest{SeriesID: series, Title: "t", Body: "b", PeriodStart: at, PeriodEnd: at})
	if err != nil {
		t.Fatal(err)
	}
	want := storedPublished(t, database, "g")
	if want != "2026-10-05 10:30:45+00:00" {
		t.Fatalf("item published = %q", want)
	}
	var start, end, created, updated string
	if err := database.QueryRow(`SELECT period_start || '', period_end || '', created_at || '', updated_at || '' FROM digests WHERE id = ?`, id).
		Scan(&start, &end, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if start != want || end != want {
		t.Errorf("period = %q .. %q, want %q", start, end, want)
	}
	for name, v := range map[string]string{"created_at": created, "updated_at": updated} {
		if len(v) != len(want) || !strings.HasSuffix(v, "+00:00") {
			t.Errorf("%s = %q, want the shape of %q", name, v, want)
		}
	}
}

func TestFirstMissingID(t *testing.T) {
	database := openTestDB(t)
	series := mustSeries(t, database, mustAssessor(t, database, "claude"), "daily")
	src := testSource(t, database, "feed")
	i1 := mustItem(t, database, src, "one")
	d1 := mustDigest(t, database, series, "d", day(5), nil, nil)

	if id, err := FirstMissingItemID(database, []int64{i1}); err != nil || id != 0 {
		t.Fatalf("FirstMissingItemID(existing) = %d, %v", id, err)
	}
	if id, err := FirstMissingItemID(database, []int64{i1, 900, 800}); err != nil || id != 900 {
		t.Fatalf("FirstMissingItemID = %d, %v; want the first missing in the order given", id, err)
	}
	if id, err := FirstMissingItemID(database, nil); err != nil || id != 0 {
		t.Fatalf("FirstMissingItemID(nil) = %d, %v", id, err)
	}
	if id, err := FirstMissingDigestID(database, []int64{d1, 77}); err != nil || id != 77 {
		t.Fatalf("FirstMissingDigestID = %d, %v", id, err)
	}
}
