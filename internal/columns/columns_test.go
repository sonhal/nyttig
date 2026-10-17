package columns

import (
	"strings"
	"testing"
)

// The same cases are in web/src/lib/columns.test.ts; keep them in step.
func TestParse(t *testing.T) {
	cases := []struct {
		in, want, wantErr string
	}{
		{"", "", ""},
		{"  ", "", ""},
		{"title,source", "source,title", ""},
		{"Title, SRC", "source,title", ""},
		{"time,title", "date,title", ""},
		{"description,scores,tag", "tags,score,desc", ""},
		{"domain,desc,title,score,tags,source,date", "", ""},
		{"title,title", "", "twice"},
		{"title,time,date", "", "twice"},
		{"title,bogus", "", "bogus"},
		{",", "", "empty"},
		{"title,", "", "empty"},
	}
	for _, tc := range cases {
		got, err := Parse(tc.in)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Parse(%q) error = %v, want one containing %q", tc.in, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.in, err)
			continue
		}
		if Format(got) != tc.want {
			t.Errorf("Parse(%q) = %q, want %q", tc.in, Format(got), tc.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	got, err := Normalize([]string{"title", "date"})
	if err != nil || Format(got) != "date,title" {
		t.Errorf("Normalize = %v, %v", got, err)
	}
	if got, err := Normalize(All); err != nil || got != nil {
		t.Errorf("Normalize(All) = %v, %v; want nil", got, err)
	}
	if got, err := Normalize(nil); err != nil || got != nil {
		t.Errorf("Normalize(nil) = %v, %v; want nil", got, err)
	}
	// Aliases are for people typing; the wire takes canonical names only.
	if _, err := Normalize([]string{"src"}); err == nil {
		t.Error("Normalize accepted an alias")
	}
	if _, err := Normalize([]string{"Title"}); err == nil {
		t.Error("Normalize accepted a name in the wrong case")
	}
}
