// Package archives keeps the files of archived articles and runs the workers
// that create them. The archives table (package db) is the index and the
// queue; the archiver package builds the page.
package archives

import (
	"compress/gzip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sonhal/nyttig/internal/server/db"
)

const tmpPrefix = ".tmp-"

// fileNamePattern matches the files this package writes: <id>.html.gz and
// <id>.pdf.gz.
var fileNamePattern = regexp.MustCompile(`^(\d+)\.(html|pdf)\.gz$`)

// ext is the file extension for a stored content type.
func ext(contentType string) string {
	if strings.HasPrefix(contentType, "application/pdf") {
		return "pdf"
	}
	return "html"
}

// FileName is the name of an archive's file inside the archive directory.
func FileName(id int64, contentType string) string {
	return strconv.FormatInt(id, 10) + "." + ext(contentType) + ".gz"
}

// WriteFile stores data gzip-compressed as the archive's file and returns its
// size on disk. It writes a temporary file, syncs it and renames it, so a
// reader never sees a partial file. A file of the other type left by an
// earlier copy is removed.
func WriteFile(dir string, id int64, contentType string, data []byte) (int64, error) {
	tmp, err := os.CreateTemp(dir, tmpPrefix+"*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after the rename

	zw := gzip.NewWriter(tmp)
	if _, err := zw.Write(data); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	info, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, FileName(id, contentType))); err != nil {
		return 0, err
	}
	other := "application/pdf"
	if ext(contentType) == "pdf" {
		other = "text/html"
	}
	_ = os.Remove(filepath.Join(dir, FileName(id, other)))
	return info.Size(), nil
}

// OpenFile opens the archive's file for reading its uncompressed bytes. A
// missing file is an error satisfying errors.Is(err, os.ErrNotExist).
func OpenFile(dir string, id int64, contentType string) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Join(dir, FileName(id, contentType)))
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("read archive %d: %w", id, err)
	}
	return &gzipFile{Reader: zr, f: f}, nil
}

type gzipFile struct {
	*gzip.Reader
	f *os.File
}

func (g *gzipFile) Close() error {
	zerr := g.Reader.Close()
	if err := g.f.Close(); err != nil {
		return err
	}
	return zerr
}

// RemoveFiles deletes the archive's file, whichever type it has.
func RemoveFiles(dir string, id int64) error {
	var errs []error
	for _, ct := range []string{"text/html", "application/pdf"} {
		if err := os.Remove(filepath.Join(dir, FileName(id, ct))); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Reconcile makes the database and the directory agree after a start:
// running archives go back to pending, archives whose file is gone lose their
// size (a saved one also becomes failed), and files without a row (and leftover temporary files) are
// deleted.
func Reconcile(database *sql.DB, dir string, logger *slog.Logger) error {
	requeued, err := db.RequeueRunningArchives(database)
	if err != nil {
		return fmt.Errorf("requeue running archives: %w", err)
	}

	saved, err := db.ListArchivesWithCopy(database)
	if err != nil {
		return fmt.Errorf("list archives with a copy: %w", err)
	}
	missing := 0
	for _, a := range saved {
		_, err := os.Stat(filepath.Join(dir, FileName(a.ID, a.ContentType)))
		if err == nil {
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat archive %d: %w", a.ID, err)
		}
		if _, err := db.MarkFileMissing(database, a.ID, "file missing"); err != nil {
			return fmt.Errorf("mark archive %d missing: %w", a.ID, err)
		}
		missing++
	}

	known, err := db.ArchiveIDs(database)
	if err != nil {
		return fmt.Errorf("list archive ids: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read archive dir: %w", err)
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		orphan := strings.HasPrefix(name, tmpPrefix)
		if m := fileNamePattern.FindStringSubmatch(name); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			orphan = !known[id]
		}
		if !orphan {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			logger.Warn("remove orphan archive file", "file", name, "error", err)
			continue
		}
		removed++
	}

	if requeued+missing+removed > 0 {
		logger.Info("archives reconciled", "requeued", requeued, "missing_files", missing, "orphan_files_removed", removed)
	}
	return nil
}
