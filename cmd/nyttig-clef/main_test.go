package main

import "testing"

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    options
		wantErr bool
	}{
		{"config only", []string{"--config", "c.toml"}, options{config: "c.toml", logLevel: "info"}, false},
		{"all flags", []string{"--config", "c.toml", "--once", "--dry-run", "--log-level", "debug"},
			options{config: "c.toml", once: true, dryRun: true, logLevel: "debug"}, false},
		{"version needs no config", []string{"-version"}, options{showVersion: true, logLevel: "info"}, false},
		{"no config", nil, options{}, true},
		{"extra arguments", []string{"--config", "c.toml", "x"}, options{}, true},
		{"unknown flag", []string{"--token", "x"}, options{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFlags(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
