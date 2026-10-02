package since

import (
	"testing"
	"time"
)

// The same cases are in web/src/lib/since.test.ts; keep them in step.
func TestCutoff(t *testing.T) {
	cases := []struct {
		now, in, want string
	}{
		{"2026-03-31T12:00:00Z", "24h", "2026-03-30T12:00:00Z"},
		{"2026-03-31T12:00:00Z", "36h", "2026-03-30T00:00:00Z"},
		{"2026-03-31T12:00:00Z", "7d", "2026-03-24T12:00:00Z"},
		{"2026-03-31T12:00:00Z", "30d", "2026-03-01T12:00:00Z"},
		{"2026-03-31T12:00:00Z", "2w", "2026-03-17T12:00:00Z"},
		{"2026-03-31T12:00:00Z", "1y", "2025-03-31T12:00:00Z"},
		// 31 March minus a month is "31 February", which normalises forward.
		{"2026-03-31T12:00:00Z", "1mo", "2026-03-03T12:00:00Z"},
		{"2024-03-31T12:00:00Z", "1mo", "2024-03-02T12:00:00Z"},
		// 29 February minus a year is "29 February" of a non-leap year.
		{"2028-02-29T12:00:00Z", "1y", "2027-03-01T12:00:00Z"},
		{"2026-01-15T00:00:00Z", "1mo", "2025-12-15T00:00:00Z"},
		{"2026-01-15T00:00:00Z", "13mo", "2024-12-15T00:00:00Z"},
		{"2026-01-15T00:00:00Z", "9999h", "2024-11-24T09:00:00Z"},
	}
	for _, tc := range cases {
		now, err := time.Parse(time.RFC3339, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		w, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		if got := w.Cutoff(now).Format(time.RFC3339); got != tc.want {
			t.Errorf("%s - %s = %s, want %s", tc.now, tc.in, got, tc.want)
		}
	}
}

func TestCutoffUsesUTC(t *testing.T) {
	now := time.Date(2026, 3, 31, 1, 0, 0, 0, time.FixedZone("", 5*3600)) // 30 March 20:00Z
	w, _ := Parse("1mo")
	if got := w.Cutoff(now).Format(time.RFC3339); got != "2026-03-02T20:00:00Z" {
		t.Errorf("got %s", got)
	}
}

func TestParse(t *testing.T) {
	good := map[string]Window{
		"1h":    {1, Hours},
		"24h":   {24, Hours},
		"7d":    {7, Days},
		"2w":    {2, Weeks},
		"1mo":   {1, Months},
		"12mo":  {12, Months},
		"1y":    {1, Years},
		"9999d": {9999, Days},
		"07d":   {7, Days},
	}
	for in, want := range good {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
		if err == nil && in[0] != '0' && got.String() != in {
			t.Errorf("String() = %q, want %q", got.String(), in)
		}
	}
	for _, in := range []string{
		"", "7", "d", "mo", "0d", "0h", "10000d", "00007d", "-1d", "+7d", "1m", "1M", "1D", "7H",
		" 7d", "7d ", "7 d", "1.5d", "7days", "7min", "7s", "1d1h", "7d\n", "٣d",
	} {
		if w, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %v, want an error", in, w)
		}
	}
}
