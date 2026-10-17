// Package columns holds the names of the feed's columns that a filter can
// show or hide. web/src/lib/columns.ts implements the same rules; both are
// tested against the same table of cases.
//
// A selection is a list of canonical names in display order. The empty list
// means every column, so a selection of all of them is normalized to nil.
package columns

import (
	"fmt"
	"slices"
	"strings"
)

// All is every column in display order.
var All = []string{"date", "source", "tags", "score", "title", "desc", "domain"}

// aliases are the other spellings Parse accepts.
var aliases = map[string]string{
	"time":        "date",
	"src":         "source",
	"tag":         "tags",
	"scores":      "score",
	"description": "desc",
}

func index(name string) int {
	for i, c := range All {
		if c == name {
			return i
		}
	}
	return -1
}

// Normalize checks a list of canonical names and returns it in display order.
// Unknown names, empty names and duplicates are errors. The full set (and the
// empty list) is nil.
func Normalize(names []string) ([]string, error) {
	seen := make([]bool, len(All))
	for _, n := range names {
		if n == "" {
			return nil, fmt.Errorf("columns: empty column name")
		}
		i := index(n)
		if i < 0 {
			return nil, fmt.Errorf("columns: unknown column %q (want one of %s)", n, strings.Join(All, ", "))
		}
		if seen[i] {
			return nil, fmt.Errorf("columns: column %q given twice", n)
		}
		seen[i] = true
	}
	var out []string
	for i, c := range All {
		if seen[i] {
			out = append(out, c)
		}
	}
	if len(out) == len(All) {
		return nil, nil
	}
	return out, nil
}

// Parse reads a comma-separated list such as "title, src". Names are trimmed
// and case-insensitive, and the aliases are accepted. "" is every column.
//
// A list whose names all start with "-" excludes them: "-desc,-domain" is
// every column but those two. Mixing shown and hidden names is an error, and
// so is hiding every column. The result is always the shown columns, so an
// exclusion is never stored as such.
func Parse(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	names := make([]string, len(parts))
	hidden := 0
	for i, p := range parts {
		n := strings.ToLower(strings.TrimSpace(p))
		if strings.HasPrefix(n, "-") {
			hidden++
			n = strings.TrimSpace(n[1:])
		}
		if a, ok := aliases[n]; ok {
			n = a
		}
		names[i] = n
	}
	if hidden == 0 {
		return Normalize(names)
	}
	if hidden != len(names) {
		return nil, fmt.Errorf("columns: don't mix shown and hidden (-name) columns")
	}
	// Checked like a list of shown columns, then turned around.
	if _, err := Normalize(names); err != nil {
		return nil, err
	}
	var shown []string
	for _, c := range All {
		if !slices.Contains(names, c) {
			shown = append(shown, c)
		}
	}
	if len(shown) == 0 {
		return nil, fmt.Errorf("columns: can't hide every column")
	}
	return Normalize(shown)
}

// Spell writes a selection the shorter way for people to read: the shown
// columns ("source,title"), or the hidden ones when fewer ("-desc,-domain").
// Parse reads both back; nil is "".
func Spell(names []string) string {
	if len(names) == 0 || 2*len(names) <= len(All) {
		return Format(names)
	}
	var hidden []string
	for _, c := range All {
		if !slices.Contains(names, c) {
			hidden = append(hidden, "-"+c)
		}
	}
	return strings.Join(hidden, ",")
}

// Format joins a selection with commas; nil is "".
func Format(names []string) string {
	return strings.Join(names, ",")
}
