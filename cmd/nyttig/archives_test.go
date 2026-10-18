package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestArchiveFileName(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    *pb.Archive
		want string
	}{
		{"slug", &pb.Archive{Id: 1, Title: "Hello, World! It's 2026", ContentType: "text/html; charset=utf-8"}, "hello-world-it-s-2026.html"},
		{"pdf", &pb.Archive{Id: 1, Title: "Paper", ContentType: "application/pdf"}, "paper.pdf"},
		{"no title", &pb.Archive{Id: 9, Title: " ../ ", ContentType: "text/html"}, "archive-9.html"},
		{"path and control chars", &pb.Archive{Id: 1, Title: "../../etc/passwd\x1b[2J"}, "etc-passwd-2j.html"},
		{"unicode", &pb.Archive{Id: 1, Title: "Rødgrøt med fløde"}, "rødgrøt-med-fløde.html"},
	} {
		if got := archiveFileName(tc.a); got != tc.want {
			t.Errorf("%s: archiveFileName = %q, want %q", tc.name, got, tc.want)
		}
	}
	long := &pb.Archive{Id: 1, Title: "aaaaaaaaaa aaaaaaaaaa aaaaaaaaaa aaaaaaaaaa aaaaaaaaaa aaaaaaaaaa aaaaaaaaaa"}
	if got := archiveFileName(long); len([]rune(got)) > 60+len(".html") {
		t.Errorf("long slug not cut: %q", got)
	}
}

func TestFormatBytes(t *testing.T) {
	for in, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 20: "5.0 MiB"} {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestArchiveDetail(t *testing.T) {
	if got := archiveDetail(&pb.Archive{Error: "HTTP\n403\x1b[2J", Note: "n"}); got != "HTTP 403[2J" {
		t.Errorf("error wins and is sanitized: %q", got)
	}
	if got := archiveDetail(&pb.Archive{Note: "little text"}); got != "little text" {
		t.Errorf("note: %q", got)
	}
}

func TestWaitArchive(t *testing.T) {
	statuses := []string{"pending", "running", "done"}
	calls := 0
	get := func(context.Context) (*pb.Archive, error) {
		s := statuses[calls]
		calls++
		return &pb.Archive{Id: 1, Status: s}, nil
	}
	a, err := waitArchive(context.Background(), get, time.Millisecond)
	if err != nil || a.Status != "done" || calls != 3 {
		t.Fatalf("waitArchive = %v, %v after %d calls", a, err, calls)
	}

	boom := errors.New("boom")
	if _, err := waitArchive(context.Background(), func(context.Context) (*pb.Archive, error) { return nil, boom }, time.Millisecond); !errors.Is(err, boom) {
		t.Errorf("error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	busy := func(context.Context) (*pb.Archive, error) { return &pb.Archive{Status: "running"}, nil }
	if _, err := waitArchive(ctx, busy, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled wait = %v", err)
	}
}

func TestOpenArchiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.html")
	f, err := openArchiveFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("old")
	_ = f.Close()
	if _, err := openArchiveFile(path, false); err == nil {
		t.Fatal("overwrote an existing file without -f")
	}
	f, err = openArchiveFile(path, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Errorf("-f did not truncate: %q", b)
	}
}
