package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sonhal/nyttig/internal/config"
	"github.com/sonhal/nyttig/internal/server/archiver"
)

const (
	defaultArchiveWorkers = 2
	maxArchiveWorkers     = 16
)

// archiveSettings resolves the archive options: dir defaults to "archives"
// next to the database, workers to 2 and maxBytes to the archiver's default.
func archiveSettings(dbPath, dir string, workers int, maxBytes int64) (string, int, archiver.Limits, error) {
	lim := archiver.DefaultLimits()
	switch {
	case workers < 0 || workers > maxArchiveWorkers:
		return "", 0, lim, fmt.Errorf("archive_workers must be between 1 and %d, got %d", maxArchiveWorkers, workers)
	case workers == 0:
		workers = defaultArchiveWorkers
	}
	if maxBytes < 0 {
		return "", 0, lim, fmt.Errorf("archive_max_bytes must be positive, got %d", maxBytes)
	}
	if maxBytes > 0 {
		lim.MaxTotalBytes = maxBytes
	}
	if dir == "" {
		dir = filepath.Join(filepath.Dir(dbPath), "archives")
	}
	return config.ExpandHome(dir), workers, lim, nil
}

// ensureArchiveDir creates the archive directory, private to the daemon's
// user.
func ensureArchiveDir(dir string) error {
	return os.MkdirAll(dir, 0o700)
}
