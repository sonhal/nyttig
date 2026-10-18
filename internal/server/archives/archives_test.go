package archives

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sonhal/nyttig/internal/server/archiver"
	"github.com/sonhal/nyttig/internal/server/db"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func openDB(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "nyttig.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	src, err := db.InsertSource(database, &db.Source{Name: "feed", URL: "http://example.com/feed", Type: "rss", RefreshSec: 60, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return database, src
}

func queue(t *testing.T, database *sql.DB, src int64, guid string) *db.Archive {
	t.Helper()
	item, _, err := db.InsertItem(database, &db.Item{SourceID: src, GUID: guid, Link: "http://example.com/" + guid, Title: guid})
	if err != nil {
		t.Fatal(err)
	}
	a, err := db.EnqueueArchive(database, item)
	if err != nil || a == nil {
		t.Fatalf("EnqueueArchive: %v, %v", a, err)
	}
	return a
}

func readAll(t *testing.T, dir string, id int64, contentType string) string {
	t.Helper()
	r, err := OpenFile(dir, id, contentType)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWriteFile_RoundTripsAndReplacesTheOtherType(t *testing.T) {
	dir := t.TempDir()
	size, err := WriteFile(dir, 7, "text/html; charset=utf-8", []byte("<p>hello</p>"))
	if err != nil || size <= 0 {
		t.Fatalf("WriteFile = %d, %v", size, err)
	}
	if got := readAll(t, dir, 7, "text/html; charset=utf-8"); got != "<p>hello</p>" {
		t.Fatalf("read back %q", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "7.html.gz")); err != nil || info.Size() != size {
		t.Fatalf("7.html.gz: %v, %v", info, err)
	}

	if _, err := WriteFile(dir, 7, "application/pdf", []byte("%PDF-1.7")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "7.html.gz")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the html copy should be gone: %v", err)
	}
	if got := readAll(t, dir, 7, "application/pdf"); got != "%PDF-1.7" {
		t.Fatalf("pdf = %q", got)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("directory has %d entries, want just 7.pdf.gz (no temporary files)", len(entries))
	}
	if _, err := OpenFile(dir, 99, "text/html"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenFile of a missing file = %v, want ErrNotExist", err)
	}
	if err := RemoveFiles(dir, 7); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFiles(dir, 7); err != nil {
		t.Fatalf("second RemoveFiles: %v", err)
	}
}

func TestReconcile(t *testing.T) {
	database, src := openDB(t)
	dir := t.TempDir()

	running := queue(t, database, src, "running")
	withFile := queue(t, database, src, "withfile")
	noFile := queue(t, database, src, "nofile")
	for range 3 {
		if _, err := db.ClaimArchive(database); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []*db.Archive{withFile, noFile} {
		if _, err := db.FinishArchive(database, a.ID, db.ArchiveResult{Status: db.ArchiveDone, ContentType: "text/html; charset=utf-8"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := WriteFile(dir, withFile.ID, "text/html", []byte("x")); err != nil {
		t.Fatal(err)
	}
	// Files without a row, a leftover temporary file, and something else.
	for _, name := range []string{"9999.html.gz", "9998.pdf.gz", ".tmp-123", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := Reconcile(database, dir, testLogger()); err != nil {
		t.Fatal(err)
	}

	if a, _ := db.GetArchive(database, running.ID); a.Status != db.ArchivePending {
		t.Errorf("running archive is %q, want pending", a.Status)
	}
	if a, _ := db.GetArchive(database, withFile.ID); a.Status != db.ArchiveDone {
		t.Errorf("archive with its file is %q, want done", a.Status)
	}
	if a, _ := db.GetArchive(database, noFile.ID); a.Status != db.ArchiveFailed || a.Error != "file missing" {
		t.Errorf("archive without its file = %q %q, want failed: file missing", a.Status, a.Error)
	}
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if len(left) != 2 || left[0] != FileName(withFile.ID, "text/html") || left[1] != "notes.txt" {
		t.Errorf("files left = %v, want the archive's own file and notes.txt", left)
	}
}

func waitStatus(t *testing.T, database *sql.DB, id int64, want string) *db.Archive {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		a, err := db.GetArchive(database, id)
		if err != nil {
			t.Fatal(err)
		}
		if a != nil && a.Status == want {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive %d is %v, want %s", id, a, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startWorker(t *testing.T, w *Worker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestWorker_CallsOnFinishedAndTruncatesErrors(t *testing.T) {
	database, src := openDB(t)
	dir := t.TempDir()
	a := queue(t, database, src, "long-error")
	long := make([]rune, 1000)
	for i := range long {
		long[i] = 'é'
	}
	finished := make(chan int64, 1)
	w := &Worker{
		DB: database, Dir: dir, Client: http.DefaultClient, Limits: archiver.DefaultLimits(), Workers: 1, Logger: testLogger(),
		Archive: func(context.Context, *http.Client, string, archiver.Limits) (*archiver.Result, error) {
			return nil, errors.New(string(long))
		},
		OnFinished: func(itemID int64) { finished <- itemID },
	}
	startWorker(t, w) // pending rows from before the start are picked up at once

	got := waitStatus(t, database, a.ID, db.ArchiveFailed)
	if n := len([]rune(got.Error)); n != maxErrorLen {
		t.Errorf("error has %d characters, want %d", n, maxErrorLen)
	}
	select {
	case id := <-finished:
		if id != a.ItemID {
			t.Errorf("OnFinished(%d), want %d", id, a.ItemID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnFinished was not called")
	}
}

func TestWorker_RemovedWhileRunningLeavesNoFile(t *testing.T) {
	database, src := openDB(t)
	dir := t.TempDir()
	a := queue(t, database, src, "removed")
	started, release := make(chan struct{}), make(chan struct{})
	w := &Worker{
		DB: database, Dir: dir, Client: http.DefaultClient, Limits: archiver.DefaultLimits(), Workers: 1, Logger: testLogger(),
		Archive: func(context.Context, *http.Client, string, archiver.Limits) (*archiver.Result, error) {
			close(started)
			<-release
			return &archiver.Result{ContentType: "text/html; charset=utf-8", Body: []byte("x")}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	<-started
	if ok, err := db.DeleteArchive(database, a.ID); err != nil || !ok {
		t.Fatalf("DeleteArchive = %v, %v", ok, err)
	}
	close(release)
	// Closing the worker waits for the job, so the file check is final.
	cancel()
	<-done
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files left for a removed archive: %v", entries)
	}
}

func TestWorker_ShutdownLeavesTheArchiveRunning(t *testing.T) {
	database, src := openDB(t)
	a := queue(t, database, src, "shutdown")
	started := make(chan struct{})
	w := &Worker{
		DB: database, Dir: t.TempDir(), Client: http.DefaultClient, Limits: archiver.DefaultLimits(), Workers: 1, Logger: testLogger(),
		Archive: func(ctx context.Context, _ *http.Client, _ string, _ archiver.Limits) (*archiver.Result, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	<-started
	cancel()
	<-done

	if got, _ := db.GetArchive(database, a.ID); got.Status != db.ArchiveRunning {
		t.Fatalf("status after shutdown = %q, want running (Reconcile requeues it)", got.Status)
	}
}

func TestWorker_RunsJobsInParallelAndWakes(t *testing.T) {
	database, src := openDB(t)
	w := &Worker{
		DB: database, Dir: t.TempDir(), Client: http.DefaultClient, Limits: archiver.DefaultLimits(), Workers: 2, Logger: testLogger(),
	}
	both := make(chan struct{}, 2)
	release := make(chan struct{})
	w.Archive = func(context.Context, *http.Client, string, archiver.Limits) (*archiver.Result, error) {
		both <- struct{}{}
		<-release
		return &archiver.Result{ContentType: "text/html; charset=utf-8", Body: []byte("x")}, nil
	}
	startWorker(t, w)
	time.Sleep(20 * time.Millisecond) // both workers idle: nothing was pending at start

	a, b := queue(t, database, src, "a"), queue(t, database, src, "b")
	w.Wake()
	for range 2 {
		select {
		case <-both:
		case <-time.After(5 * time.Second):
			t.Fatal("the two archives did not run at once")
		}
	}
	close(release)
	waitStatus(t, database, a.ID, db.ArchiveDone)
	waitStatus(t, database, b.ID, db.ArchiveDone)
}
