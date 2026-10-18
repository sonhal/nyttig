package main

import (
	"path/filepath"
	"testing"

	"github.com/sonhal/nyttig/internal/server/archiver"
)

func TestArchiveSettings(t *testing.T) {
	def := archiver.DefaultLimits()
	tests := []struct {
		name         string
		dir          string
		workers      int
		maxBytes     int64
		wantDir      string
		wantWorkers  int
		wantMaxTotal int64
		wantErr      bool
	}{
		{name: "defaults", wantDir: "/var/lib/nyttig/archives", wantWorkers: 2, wantMaxTotal: def.MaxTotalBytes},
		{name: "set", dir: "/srv/a", workers: 4, maxBytes: 1 << 20, wantDir: "/srv/a", wantWorkers: 4, wantMaxTotal: 1 << 20},
		{name: "too many workers", workers: 17, wantErr: true},
		{name: "negative workers", workers: -1, wantErr: true},
		{name: "negative size", maxBytes: -1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, workers, lim, err := archiveSettings("/var/lib/nyttig/nyttig.db", tc.dir, tc.workers, tc.maxBytes)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if filepath.ToSlash(dir) != tc.wantDir || workers != tc.wantWorkers || lim.MaxTotalBytes != tc.wantMaxTotal {
				t.Errorf("got %q, %d, %d; want %q, %d, %d", dir, workers, lim.MaxTotalBytes, tc.wantDir, tc.wantWorkers, tc.wantMaxTotal)
			}
		})
	}
}
