package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestFindSeries(t *testing.T) {
	list := []*pb.DigestSeries{
		{Id: 1, AssessorName: "claude", Name: "daily-cve"},
		{Id: 2, AssessorName: "gpt", Name: "daily-cve"},
		{Id: 3, AssessorName: "a/b", Name: "monthly"},
		{Id: 12, AssessorName: "claude", Name: "7"},
	}
	for ref, want := range map[string]int64{
		"1":                1,
		"12":               12,
		"claude/daily-cve": 1,
		"claude/DAILY-cve": 1,
		"gpt/daily-cve":    2,
		"a/b/monthly":      3,
		"claude/7":         12,
	} {
		got, err := findSeries(list, ref)
		if err != nil || got.Id != want {
			t.Errorf("findSeries(%q) = %v, %v; want series %d", ref, got, err, want)
		}
	}
	for _, ref := range []string{"", "99", "daily-cve", "CLAUDE/daily-cve", "claude/", "gpt/monthly"} {
		if got, err := findSeries(list, ref); err == nil {
			t.Errorf("findSeries(%q) = %v, want an error", ref, got)
		}
	}
}

func TestParseTimeArg(t *testing.T) {
	for _, tc := range []struct {
		in   string
		end  bool
		want time.Time
	}{
		{"2026-10-05", false, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)},
		{"2026-10-05", true, time.Date(2026, 10, 5, 23, 59, 59, 0, time.UTC)},
		{"2026-10-05T12:30:00Z", true, time.Date(2026, 10, 5, 12, 30, 0, 0, time.UTC)},
		{"2026-10-05T14:30:00+02:00", false, time.Date(2026, 10, 5, 12, 30, 0, 0, time.UTC)},
	} {
		got, err := parseTimeArg(tc.in, tc.end)
		if err != nil || !got.Equal(tc.want) || got.Location() != time.UTC {
			t.Errorf("parseTimeArg(%q, %v) = %v, %v; want %v", tc.in, tc.end, got, err, tc.want)
		}
	}
	for _, in := range []string{"", "yesterday", "2026-13-01", "2026-10-05 12:00", "10/05/2026"} {
		if _, err := parseTimeArg(in, false); err == nil {
			t.Errorf("parseTimeArg(%q) accepted", in)
		}
	}
}

func TestParseIDList(t *testing.T) {
	got, err := parseIDList("1, 2,3")
	if err != nil || !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Errorf("got %v, %v", got, err)
	}
	// Empty is an empty, non-nil list: update-digest sends it to clear.
	got, err = parseIDList("")
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("empty = %#v, %v", got, err)
	}
	for _, in := range []string{"1,,2", "x", "0", "-1", "1,2,"} {
		if _, err := parseIDList(in); err == nil {
			t.Errorf("parseIDList(%q) accepted", in)
		}
	}
}

func TestReadBodyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte("# Hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readBodyFile(path, nil); err != nil || got != "# Hi\n" {
		t.Errorf("file = %q, %v", got, err)
	}
	if got, err := readBodyFile("-", strings.NewReader("from stdin")); err != nil || got != "from stdin" {
		t.Errorf("stdin = %q, %v", got, err)
	}
	if _, err := readBodyFile(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Error("missing file accepted")
	}
	// An oversized body is cut one byte past the daemon's limit, so the
	// daemon reports it instead of the CLI reading it all.
	got, _ := readBodyFile("-", strings.NewReader(strings.Repeat("x", 200000)))
	if len(got) != 64*1024+1 {
		t.Errorf("read %d bytes", len(got))
	}
}

func TestFormatDigest_StripsControlCharacters(t *testing.T) {
	ts := timestamppb.New(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	d := &pb.Digest{
		Id: 7, Title: "T\x1b[31mitle", SeriesName: "dai\x07ly", AssessorName: "cla\x00ude",
		Body:        "# Head\r\x1b]0;pwned\x07\n- one\ttab\n\x9b2J end",
		PeriodStart: ts, PeriodEnd: ts, CreatedAt: ts, UpdatedAt: ts,
		Items:  []*pb.DigestItem{{ItemId: 5, Title: "i\x1b[2Jtem", Link: "https://example.com/\x1b", SourceName: "feed"}},
		Inputs: []*pb.DigestRef{{Id: 3, Title: "old\x1b", SeriesName: "monthly", PeriodEnd: ts}},
	}
	out := formatDigest(d)
	for _, r := range out {
		if r != '\n' && r != '\t' && (r < 0x20 || (r >= 0x7f && r < 0xa0)) {
			t.Fatalf("control character %q in output:\n%q", r, out)
		}
	}
	for _, want := range []string{"Digest 7: T[31mitle", "claude/daily", "# Head", "\n- one\ttab\n", "Based on 1 items", "[#5] i[2Jtem", "[3] old (monthly, 2026-10-05 00:00Z)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
