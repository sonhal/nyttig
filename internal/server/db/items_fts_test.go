package db

import "testing"

func TestFtsQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"hello", "hello", `"hello"`},
		{"a OR b", "a OR b", `"a OR b"`},
		{`he said "hi"`, `he said "hi"`, `"he said ""hi"""`},
		{"wildcard", "*", `"*"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ftsQuote(tt.input); got != tt.want {
				t.Errorf("ftsQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
