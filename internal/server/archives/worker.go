package archives

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"unicode/utf8"

	"github.com/sonhal/nyttig/internal/server/archiver"
	"github.com/sonhal/nyttig/internal/server/db"
)

// maxErrorLen bounds the reason stored with a failed archive.
const maxErrorLen = 300

// ArchiveFunc builds the archive of a page. archiver.Archive is the real one;
// tests pass a fake.
type ArchiveFunc func(ctx context.Context, client *http.Client, pageURL string, lim archiver.Limits) (*archiver.Result, error)

// Worker takes pending archives from the database and saves them.
type Worker struct {
	DB      *sql.DB
	Dir     string
	Client  *http.Client
	Archive ArchiveFunc
	Limits  archiver.Limits
	Workers int // at least 1
	Logger  *slog.Logger

	// OnFinished is called after an archive reaches done, partial or failed,
	// with the ID of its item (never 0).
	OnFinished func(itemID int64)

	once sync.Once
	wake chan struct{}
}

func (w *Worker) init() {
	w.once.Do(func() { w.wake = make(chan struct{}, max(w.Workers, 1)) })
}

// Wake tells idle workers to look for pending archives. It never blocks.
func (w *Worker) Wake() {
	w.init()
	for range cap(w.wake) {
		select {
		case w.wake <- struct{}{}:
		default:
			return
		}
	}
}

// Run starts the workers and returns when ctx is done and they have stopped.
// An archive running at that point stays running in the database; the next
// start's Reconcile queues it again.
func (w *Worker) Run(ctx context.Context) {
	w.init()
	var wg sync.WaitGroup
	for range max(w.Workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx)
		}()
	}
	wg.Wait()
}

func (w *Worker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		a, err := db.ClaimArchive(w.DB)
		if err != nil {
			w.Logger.Error("claim archive", "error", err)
		}
		if a == nil {
			select {
			case <-ctx.Done():
				return
			case <-w.wake:
			}
			continue
		}
		w.process(ctx, a)
	}
}

func (w *Worker) process(ctx context.Context, a *db.Archive) {
	res, err := w.Archive(ctx, w.Client, a.Link, w.Limits)
	if ctx.Err() != nil {
		return // shutting down; Reconcile requeues it
	}
	var out db.ArchiveResult
	if err == nil {
		out, err = w.save(a, res)
	}
	if err != nil {
		out = db.ArchiveResult{Status: db.ArchiveFailed, Error: shorten(err.Error())}
		w.Logger.Info("archive failed", "archive_id", a.ID, "link", a.Link, "error", err)
	}

	ok, ferr := db.FinishArchive(w.DB, a.ID, out)
	switch {
	case ferr != nil:
		w.Logger.Error("finish archive", "archive_id", a.ID, "error", ferr)
		return
	case !ok:
		// Removed while it ran: its file has no row.
		if err := RemoveFiles(w.Dir, a.ID); err != nil {
			w.Logger.Warn("remove file of a removed archive", "archive_id", a.ID, "error", err)
		}
		return
	}
	if w.OnFinished != nil && a.ItemID != 0 {
		w.OnFinished(a.ItemID)
	}
}

// save writes the result's file and returns what to record.
func (w *Worker) save(a *db.Archive, res *archiver.Result) (db.ArchiveResult, error) {
	if res == nil {
		return db.ArchiveResult{}, errors.New("archiver returned no page")
	}
	size, err := WriteFile(w.Dir, a.ID, res.ContentType, res.Body)
	if err != nil {
		return db.ArchiveResult{}, errors.New("cannot save the file: " + err.Error())
	}
	status := db.ArchiveDone
	if res.MissingAssets > 0 {
		status = db.ArchivePartial
	}
	return db.ArchiveResult{
		Status:        status,
		Note:          res.Note,
		FinalURL:      res.FinalURL,
		ContentType:   res.ContentType,
		HTTPStatus:    res.HTTPStatus,
		Size:          size,
		OriginalSize:  int64(len(res.Body)),
		MissingAssets: res.MissingAssets,
	}, nil
}

// shorten trims an error message to maxErrorLen characters.
func shorten(s string) string {
	if utf8.RuneCountInString(s) <= maxErrorLen {
		return s
	}
	r := []rune(s)
	return string(r[:maxErrorLen-1]) + "…"
}
