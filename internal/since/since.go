// Package since parses rolling time windows such as "24h", "7d", "2w", "1mo"
// and "1y" and turns them into a cutoff time. web/src/lib/since.ts implements
// the same rules; both are tested against the same table of cases.
package since

import (
	"fmt"
	"time"
)

// Unit is the unit of a Window.
type Unit string

const (
	Hours  Unit = "h"
	Days   Unit = "d"
	Weeks  Unit = "w"
	Months Unit = "mo"
	Years  Unit = "y"
)

// MaxCount is the largest count a window accepts.
const MaxCount = 9999

// Window is a duration counted back from now. Months and years are calendar
// units.
type Window struct {
	N    int
	Unit Unit
}

// Parse reads "<n><unit>": n is 1 to 9999 without sign or leading spaces, the
// unit is h, d, w, mo or y in lower case. "m" is rejected because it could
// mean minutes or months.
func Parse(s string) (Window, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i == len(s) {
		return Window{}, fmt.Errorf("since must look like 24h, 7d, 2w, 1mo or 1y, got %q", s)
	}
	if i > 4 {
		return Window{}, fmt.Errorf("since count must be between 1 and %d, got %q", MaxCount, s)
	}
	n := 0
	for _, c := range s[:i] {
		n = n*10 + int(c-'0')
	}
	if n < 1 || n > MaxCount {
		return Window{}, fmt.Errorf("since count must be between 1 and %d, got %q", MaxCount, s)
	}
	switch u := Unit(s[i:]); u {
	case Hours, Days, Weeks, Months, Years:
		return Window{N: n, Unit: u}, nil
	case "m":
		return Window{}, fmt.Errorf("since unit %q is ambiguous: use mo for months or h for hours", "m")
	default:
		return Window{}, fmt.Errorf("since unit must be h, d, w, mo or y, got %q", s[i:])
	}
}

// String returns the spelling Parse accepts.
func (w Window) String() string { return fmt.Sprintf("%d%s", w.N, w.Unit) }

// Cutoff returns the start of the window that ends at now. Hours, days and
// weeks are fixed lengths of 24-hour days in UTC; months and years use
// calendar arithmetic in UTC with time.AddDate's normalisation (31 March
// minus one month is 3 March in a non-leap year, 2 March in a leap year).
func (w Window) Cutoff(now time.Time) time.Time {
	now = now.UTC()
	switch w.Unit {
	case Hours:
		return now.Add(-time.Duration(w.N) * time.Hour)
	case Days:
		return now.AddDate(0, 0, -w.N)
	case Weeks:
		return now.AddDate(0, 0, -7*w.N)
	case Months:
		return now.AddDate(0, -w.N, 0)
	case Years:
		return now.AddDate(-w.N, 0, 0)
	}
	return now
}
