// Package columns holds the names of the feed's columns that a filter can
// show or hide. web/src/lib/columns.ts implements the same rules; both are
// tested against the same table of cases.
//
// A selection is a list of canonical names in display order. The empty list
// means every column, so a selection of all of them is normalized to nil.
package columns

import (
	"fmt"
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
func Parse(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	names := make([]string, len(parts))
	for i, p := range parts {
		n := strings.ToLower(strings.TrimSpace(p))
		if a, ok := aliases[n]; ok {
			n = a
		}
		names[i] = n
	}
	return Normalize(names)
}

// Format joins a selection with commas; nil is "".
func Format(names []string) string {
	return strings.Join(names, ",")
}
