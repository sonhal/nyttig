package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func newTestTable(width int) Table {
	tb := NewTable()
	tb.SetSize(width, 10)
	return tb
}

func tsAt(tm time.Time) *timestamppb.Timestamp { return timestamppb.New(tm) }

func TestFormatDate(t *testing.T) {
	cases := []struct {
		name string
		in   *timestamppb.Timestamp
		want string
	}{
		{"valid", tsAt(time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)), "30.05 10:00"},
		{"nil", nil, strings.Repeat(" ", dateColumnWidth)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatDate(c.in)
			if got != c.want {
				t.Errorf("formatDate() = %q, want %q", got, c.want)
			}
			if len(got) != dateColumnWidth {
				t.Errorf("formatDate() width = %d, want %d", len(got), dateColumnWidth)
			}
		})
	}
}

func TestTruncateWithSuffix(t *testing.T) {
	if got := truncateWithSuffix("hello world", 8, "..."); got != "hello..." {
		t.Errorf("truncateWithSuffix = %q, want %q", got, "hello...")
	}
	if got := truncateWithSuffix("short", 20, "..."); got != "short" {
		t.Errorf("truncateWithSuffix (no overflow) = %q, want %q", got, "short")
	}
	if got := truncateEllipsis("hello world", 7); got != "hello.." {
		t.Errorf("truncateEllipsis = %q, want %q", got, "hello..")
	}
}

func TestRenderRow_ShowsDate(t *testing.T) {
	tb := newTestTable(120)
	item := &pb.Item{
		Title:     "Some headline",
		Link:      "https://example.com/x",
		Viewed:    true,
		Published: tsAt(time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)),
	}
	row := stripANSI(tb.renderRow(item, false))
	if !strings.Contains(row, "30.05 10:00") {
		t.Errorf("expected row to contain date %q, got %q", "30.05 10:00", row)
	}
}

func TestRenderRow_TruncatesDescription(t *testing.T) {
	tb := newTestTable(120)
	item := &pb.Item{
		Title:       "Hi",
		Link:        "https://example.com/x",
		Viewed:      true,
		Description: strings.Repeat("x", 400),
	}
	row := stripANSI(tb.renderRow(item, false))
	if !strings.Contains(row, "...") {
		t.Errorf("expected long description truncated with '...', got %q", row)
	}
}

func TestRenderRow_DomainRightAligned(t *testing.T) {
	tb := newTestTable(120)
	item := &pb.Item{
		Title:       "Headline",
		Link:        "https://www.nrk.no/nyheter/article",
		Viewed:      true,
		Description: "A short description",
		Published:   tsAt(time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)),
	}
	row := strings.TrimRight(stripANSI(tb.renderRow(item, false)), " ")
	if !strings.HasSuffix(row, "nrk.no") {
		t.Errorf("expected row to end with right-aligned domain 'nrk.no', got %q", row)
	}
}

func TestRenderRow_NoDateNoDescription(t *testing.T) {
	tb := newTestTable(120)
	item := &pb.Item{
		Title:  "Some headline",
		Link:   "https://example.com/x",
		Viewed: true,
	}
	row := stripANSI(tb.renderRow(item, false))
	if !strings.Contains(row, "Some headline") {
		t.Errorf("expected title in row, got %q", row)
	}
	if !strings.Contains(row, "example.com") {
		t.Errorf("expected domain in row, got %q", row)
	}
	if strings.Contains(row, "...") {
		t.Errorf("did not expect '...' for empty description, got %q", row)
	}
}
