package db

import (
	"database/sql"
	"regexp"
	"testing"
)

func archiveTestItem(t *testing.T, database *sql.DB, sourceID int64, guid string) int64 {
	t.Helper()
	id, _, err := InsertItem(database, &Item{SourceID: sourceID, GUID: guid, Link: "http://example.com/" + guid, Title: "Title " + guid})
	if err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	return id
}

func mustEnqueue(t *testing.T, database *sql.DB, itemID int64) *Archive {
	t.Helper()
	a, err := EnqueueArchive(database, itemID)
	if err != nil || a == nil {
		t.Fatalf("EnqueueArchive(%d) = %v, %v", itemID, a, err)
	}
	return a
}

func TestEnqueueArchive_CopiesTheItemAndIsUniquePerItem(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	item := archiveTestItem(t, database, src, "a")

	a := mustEnqueue(t, database, item)
	if a.Status != ArchivePending || a.ItemID != item || a.Link != "http://example.com/a" ||
		a.Title != "Title a" || a.SourceName != "feed" || a.StartedAt != nil || a.FinishedAt != nil {
		t.Fatalf("archive = %+v", a)
	}

	// Pending or running: queueing again returns the same row unchanged.
	again := mustEnqueue(t, database, item)
	if again.ID != a.ID || again.Status != ArchivePending {
		t.Fatalf("second enqueue = %+v, want archive %d pending", again, a.ID)
	}
	if _, err := ClaimArchive(database); err != nil {
		t.Fatal(err)
	}
	if got := mustEnqueue(t, database, item); got.ID != a.ID || got.Status != ArchiveRunning {
		t.Fatalf("enqueue of a running archive = %+v, want it still running", got)
	}

	if a, err := EnqueueArchive(database, item+1000); err != nil || a != nil {
		t.Fatalf("EnqueueArchive(unknown item) = %v, %v; want nil, nil", a, err)
	}
}

func TestEnqueueArchive_RequeuesAFinishedArchiveKeepingItsFileFacts(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	item := archiveTestItem(t, database, src, "a")
	a := mustEnqueue(t, database, item)
	if _, err := ClaimArchive(database); err != nil {
		t.Fatal(err)
	}
	ok, err := FinishArchive(database, a.ID, ArchiveResult{Status: ArchivePartial, Note: "little text",
		FinalURL: "http://example.com/final", ContentType: "text/html; charset=utf-8", HTTPStatus: 200,
		Size: 10, OriginalSize: 40, MissingAssets: 2})
	if err != nil || !ok {
		t.Fatalf("FinishArchive = %v, %v", ok, err)
	}

	if _, err := database.Exec(`UPDATE items SET title = 'New title' WHERE id = ?`, item); err != nil {
		t.Fatal(err)
	}
	got := mustEnqueue(t, database, item)
	if got.ID != a.ID || got.Status != ArchivePending || got.Note != "" || got.Error != "" ||
		got.Title != "New title" || got.FinishedAt != nil || got.StartedAt != nil {
		t.Fatalf("requeued = %+v", got)
	}
	// The old file stays until the new copy replaces it.
	if got.Size != 10 || got.ContentType != "text/html; charset=utf-8" {
		t.Fatalf("requeued lost the old file's facts: %+v", got)
	}
}

func TestClaimArchive_TakesTheOldestPendingOnce(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	var ids []int64
	for _, g := range []string{"a", "b", "c"} {
		ids = append(ids, mustEnqueue(t, database, archiveTestItem(t, database, src, g)).ID)
	}
	// Same-second requests fall back to the ID; make "b" the oldest.
	if _, err := database.Exec(`UPDATE archives SET requested_at = '2020-01-01 00:00:00+00:00' WHERE id = ?`, ids[1]); err != nil {
		t.Fatal(err)
	}

	var order []int64
	for {
		a, err := ClaimArchive(database)
		if err != nil {
			t.Fatal(err)
		}
		if a == nil {
			break
		}
		if a.Status != ArchiveRunning || a.StartedAt == nil {
			t.Fatalf("claimed %+v, want running with started_at", a)
		}
		order = append(order, a.ID)
	}
	want := []int64{ids[1], ids[0], ids[2]}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Fatalf("claim order = %v, want %v", order, want)
	}
}

func TestFinishArchive_OnlyWhileRunning(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	a := mustEnqueue(t, database, archiveTestItem(t, database, src, "a"))

	if ok, err := FinishArchive(database, a.ID, ArchiveResult{Status: ArchiveDone}); err != nil || ok {
		t.Fatalf("finish of a pending archive = %v, %v; want false, nil", ok, err)
	}
	if _, err := FinishArchive(database, a.ID, ArchiveResult{Status: ArchivePending}); err == nil {
		t.Fatal("finish with a non-final status succeeded")
	}
	if _, err := ClaimArchive(database); err != nil {
		t.Fatal(err)
	}
	if ok, err := FinishArchive(database, a.ID, ArchiveResult{Status: ArchiveFailed, Error: "HTTP 403", HTTPStatus: 403}); err != nil || !ok {
		t.Fatalf("finish = %v, %v", ok, err)
	}
	got, err := GetArchive(database, a.ID)
	if err != nil || got == nil || got.Status != ArchiveFailed || got.Error != "HTTP 403" || got.HTTPStatus != 403 || got.FinishedAt == nil {
		t.Fatalf("archive = %+v, %v", got, err)
	}

	// Removed while running: nothing to write.
	b := mustEnqueue(t, database, archiveTestItem(t, database, src, "b"))
	if _, err := ClaimArchive(database); err != nil {
		t.Fatal(err)
	}
	if ok, err := DeleteArchive(database, b.ID); err != nil || !ok {
		t.Fatalf("DeleteArchive = %v, %v", ok, err)
	}
	if ok, err := FinishArchive(database, b.ID, ArchiveResult{Status: ArchiveDone}); err != nil || ok {
		t.Fatalf("finish of a removed archive = %v, %v; want false, nil", ok, err)
	}
	if ok, err := DeleteArchive(database, b.ID); err != nil || ok {
		t.Fatalf("second DeleteArchive = %v, %v; want false, nil", ok, err)
	}
}

func TestArchiveDates_AreUTCWholeSeconds(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	a := mustEnqueue(t, database, archiveTestItem(t, database, src, "a"))
	if _, err := ClaimArchive(database); err != nil {
		t.Fatal(err)
	}
	if _, err := FinishArchive(database, a.ID, ArchiveResult{Status: ArchiveDone}); err != nil {
		t.Fatal(err)
	}
	var requested, started, finished string
	if err := database.QueryRow(`SELECT requested_at || '', started_at || '', finished_at || '' FROM archives WHERE id = ?`, a.ID).
		Scan(&requested, &started, &finished); err != nil {
		t.Fatal(err)
	}
	shape := regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\+00:00$`)
	for name, v := range map[string]string{"requested_at": requested, "started_at": started, "finished_at": finished} {
		if !shape.MatchString(v) {
			t.Errorf("%s = %q, want YYYY-MM-DD HH:MM:SS+00:00", name, v)
		}
	}
}

func TestListArchives_FiltersAndPagesNewestFirst(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	var ids []int64
	for _, g := range []string{"a", "b", "c", "d", "e"} {
		ids = append(ids, mustEnqueue(t, database, archiveTestItem(t, database, src, g)).ID)
	}
	if _, err := database.Exec(`UPDATE archives SET status = 'failed' WHERE id IN (?, ?)`, ids[0], ids[3]); err != nil {
		t.Fatal(err)
	}

	page, more, err := ListArchives(database, "", 0, 2)
	if err != nil || !more || len(page) != 2 || page[0].ID != ids[4] || page[1].ID != ids[3] {
		t.Fatalf("first page = %v more=%v err=%v", archiveIDs(page), more, err)
	}
	page, more, err = ListArchives(database, "", page[1].ID, 10)
	if err != nil || more || archiveIDs(page)[0] != ids[2] || len(page) != 3 {
		t.Fatalf("next page = %v more=%v err=%v", archiveIDs(page), more, err)
	}
	failed, more, err := ListArchives(database, ArchiveFailed, 0, 0)
	if err != nil || more || len(failed) != 2 || failed[0].ID != ids[3] || failed[1].ID != ids[0] {
		t.Fatalf("failed = %v more=%v err=%v", archiveIDs(failed), more, err)
	}
}

func archiveIDs(list []*Archive) []int64 {
	ids := make([]int64, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	return ids
}

func TestDeleteSource_KeepsTheArchiveWithoutItsItem(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	item := archiveTestItem(t, database, src, "a")
	a := mustEnqueue(t, database, item)

	if ok, err := DeleteSource(database, src); err != nil || !ok {
		t.Fatalf("DeleteSource = %v, %v", ok, err)
	}
	got, err := GetArchive(database, a.ID)
	if err != nil || got == nil {
		t.Fatalf("archive after source delete = %v, %v", got, err)
	}
	if got.ItemID != 0 || got.Link != "http://example.com/a" || got.Title != "Title a" || got.SourceName != "feed" {
		t.Fatalf("archive = %+v, want item_id 0 and the copied fields", got)
	}
}

func TestItemArchive_InGetItemAndListItems(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	with := archiveTestItem(t, database, src, "with")
	without := archiveTestItem(t, database, src, "without")
	a := mustEnqueue(t, database, with)

	items, _, err := ListItems(database, ItemFilter{})
	if err != nil || len(items) != 2 {
		t.Fatalf("ListItems = %d items, %v", len(items), err)
	}
	for _, it := range items {
		switch it.ID {
		case with:
			if it.Archive == nil || it.Archive.ID != a.ID || it.Archive.Status != ArchivePending {
				t.Errorf("archived item: Archive = %+v", it.Archive)
			}
		case without:
			if it.Archive != nil {
				t.Errorf("unarchived item: Archive = %+v", it.Archive)
			}
		}
	}

	// Search goes through ListItems.
	found, _, err := SearchItems(database, "with", 0, 0, 10, 0)
	if err != nil || len(found) == 0 {
		t.Fatalf("SearchItems = %v, %v", found, err)
	}
	for _, it := range found {
		if it.ID == with && (it.Archive == nil || it.Archive.ID != a.ID) {
			t.Errorf("search result lost Archive: %+v", it.Archive)
		}
	}

	one, err := GetItem(database, with)
	if err != nil || one.Archive == nil || one.Archive.ID != a.ID {
		t.Fatalf("GetItem archive = %+v, %v", one, err)
	}
}

func TestArchiveReconcileQueries(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	running := mustEnqueue(t, database, archiveTestItem(t, database, src, "run"))
	saved := mustEnqueue(t, database, archiveTestItem(t, database, src, "saved"))
	for range 2 {
		if _, err := ClaimArchive(database); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := FinishArchive(database, saved.ID, ArchiveResult{Status: ArchiveDone}); err != nil {
		t.Fatal(err)
	}

	if n, err := RequeueRunningArchives(database); err != nil || n != 1 {
		t.Fatalf("RequeueRunningArchives = %d, %v; want 1", n, err)
	}
	if got, _ := GetArchive(database, running.ID); got.Status != ArchivePending || got.StartedAt != nil {
		t.Fatalf("requeued = %+v", got)
	}

	list, err := ListSavedArchives(database)
	if err != nil || len(list) != 1 || list[0].ID != saved.ID {
		t.Fatalf("ListSavedArchives = %v, %v", archiveIDs(list), err)
	}
	if ok, err := FailArchive(database, saved.ID, "file missing"); err != nil || !ok {
		t.Fatalf("FailArchive = %v, %v", ok, err)
	}
	if ok, err := FailArchive(database, running.ID, "x"); err != nil || ok {
		t.Fatalf("FailArchive of a pending archive = %v, %v; want false", ok, err)
	}
	if got, _ := GetArchive(database, saved.ID); got.Status != ArchiveFailed || got.Error != "file missing" {
		t.Fatalf("failed = %+v", got)
	}

	ids, err := ArchiveIDs(database)
	if err != nil || len(ids) != 2 || !ids[running.ID] || !ids[saved.ID] {
		t.Fatalf("ArchiveIDs = %v, %v", ids, err)
	}
}
