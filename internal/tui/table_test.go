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

// An item without a published date shows its fetch time instead; with
// both, the published date wins.
func TestRenderRow_FetchTimeWithoutPublished(t *testing.T) {
	tb := newTestTable(120)
	fetched := tsAt(time.Date(2026, 10, 2, 22, 40, 0, 0, time.UTC))
	item := &pb.Item{Title: "Undated", Link: "https://example.com/x", Viewed: true, FetchedAt: fetched}
	if row := stripANSI(tb.renderRow(item, false)); !strings.Contains(row, "02.10 22:40") {
		t.Errorf("expected the fetch time in the row, got %q", row)
	}
	item.Published = tsAt(time.Date(2026, 10, 1, 9, 5, 0, 0, time.UTC))
	row := stripANSI(tb.renderRow(item, false))
	if !strings.Contains(row, "01.10 09:05") || strings.Contains(row, "02.10 22:40") {
		t.Errorf("expected only the published date in the row, got %q", row)
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

func TestRenderRow_ShowsSourceChip(t *testing.T) {
	tb := newTestTable(120)
	tb.SetSourceMeta(map[int64]sourceDisplay{
		1: {Name: "HN", Color: "#FF6600"},
	})
	item := &pb.Item{
		SourceId:  1,
		Title:     "Some headline",
		Link:      "https://example.com/x",
		Viewed:    true,
		Published: tsAt(time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)),
	}
	row := stripANSI(tb.renderRow(item, false))
	// Source chip [HN] should appear between date and title.
	if !strings.Contains(row, "[HN]") {
		t.Errorf("expected source chip [HN] in row, got %q", row)
	}
	// Date should still be there.
	if !strings.Contains(row, "30.05 10:00") {
		t.Errorf("expected date in row, got %q", row)
	}
	// Title should still be there.
	if !strings.Contains(row, "Some headline") {
		t.Errorf("expected title in row, got %q", row)
	}
}

func TestRenderRow_SourceChipWithColor(t *testing.T) {
	tb := newTestTable(120)
	tb.SetSourceMeta(map[int64]sourceDisplay{
		1: {Name: "HN", Color: "#FF6600"},
	})
	item := &pb.Item{
		SourceId: 1,
		Title:    "Test",
		Link:     "https://example.com/x",
		Viewed:   true,
	}
	row := tb.renderRow(item, false)
	// The bracketed source chip [HN] should be present (with or without ANSI).
	if !strings.Contains(stripANSI(row), "[HN]") {
		t.Errorf("expected source chip [HN] in row, got %q", stripANSI(row))
	}
	// The title should also be present.
	if !strings.Contains(stripANSI(row), "Test") {
		t.Errorf("expected title in row, got %q", stripANSI(row))
	}
}

func TestRenderRow_SourceChipWithAbbreviation(t *testing.T) {
	tb := newTestTable(120)
	// Abbreviation takes precedence; full name is "Hacker News" but display is "HN".
	tb.SetSourceMeta(map[int64]sourceDisplay{
		1: {Name: "HN", Color: ""},
	})
	item := &pb.Item{
		SourceId: 1,
		Title:    "Test",
		Link:     "https://example.com/x",
		Viewed:   true,
	}
	row := stripANSI(tb.renderRow(item, false))
	if !strings.Contains(row, "[HN]") {
		t.Errorf("expected abbreviation [HN] in row, got %q", row)
	}
	if strings.Contains(row, "Hacker News") {
		t.Errorf("expected abbreviation not full name, got %q", row)
	}
}

func TestRenderRow_SourceChipMissingMeta(t *testing.T) {
	tb := newTestTable(120)
	tb.SetSourceMeta(map[int64]sourceDisplay{
		1: {Name: "HN"},
	})
	item := &pb.Item{
		SourceId:  2, // ID not in sourceMeta
		Title:     "Some headline",
		Link:      "https://example.com/x",
		Viewed:    true,
		Published: tsAt(time.Date(2026, 5, 30, 10, 0, 0, 0, time.UTC)),
	}
	row := stripANSI(tb.renderRow(item, false))
	// No source chip for unknown source_id.
	if strings.Contains(row, "[") {
		t.Errorf("did not expect any bracket chip for missing source meta, got %q", row)
	}
	// But title and date should still render.
	if !strings.Contains(row, "Some headline") {
		t.Errorf("expected title in row, got %q", row)
	}
	if !strings.Contains(row, "30.05 10:00") {
		t.Errorf("expected date in row, got %q", row)
	}
}

func TestSanitizeLine(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "Hello world", "Hello world"},
		{"newlines and tabs", "Go 1.27\twith\nnewlines\r\n", "Go 1.27 with newlines"},
		{"ESC sequence", "a\x1b[2Jb", "a[2Jb"},
		{"C1 CSI", "a\u009b31mb", "a31mb"},
		{"keeps unicode", "Æøå — 日本", "Æøå — 日本"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeLine(tt.in); got != tt.want {
				t.Errorf("SanitizeLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRenderRow_SingleLineWithoutControlChars(t *testing.T) {
	tb := newTestTable(120)
	item := &pb.Item{
		Id:          1,
		Title:       "Go 1.27\twith\nnewlines",
		Description: "evil \x1b[2J\x1b]0;pwned\x07 text",
		Link:        "https://example.com/a",
	}
	row := tb.renderRow(item, false)
	plain := stripANSI(row)
	if strings.Contains(plain, "\n") {
		t.Errorf("row spans multiple lines: %q", plain)
	}
	if !strings.Contains(plain, "Go 1.27 with newlines") {
		t.Errorf("row missing flattened title: %q", plain)
	}
	// Only lipgloss's own SGR color sequences may remain; the feed's clear
	// screen and OSC title-set sequences must not.
	if strings.Contains(row, "\x1b[2J") || strings.Contains(row, "\x1b]") || strings.Contains(row, "\x07") {
		t.Errorf("row contains injected escape sequences: %q", row)
	}
}

func TestSanitizeText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain\nlines\tand tabs", "plain\nlines\tand tabs"},
		{"red \x1b[31mtext\x1b[0m", "red [31mtext[0m"},
		{"a\x1b]0;title\x07b", "a]0;titleb"},
		{"cr\r\nlf", "cr\nlf"},
		{"c1 \u009b2J \u0085 end", "c1 2J  end"},
		{"nul\x00here", "nulhere"},
		{"héllo ✓ 日本語", "héllo ✓ 日本語"},
	} {
		if got := SanitizeText(tc.in); got != tc.want {
			t.Errorf("SanitizeText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
