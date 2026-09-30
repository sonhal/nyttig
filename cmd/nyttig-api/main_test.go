package main

import (
	"strings"
	"testing"
)

func TestParseFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"defaults with origin", []string{"--origin", "https://nyttig.example.com"}, ""},
		{"origin required", nil, "--origin is required"},
		{"bad origin", []string{"--origin", "nyttig.example.com"}, "--origin"},
		{"public listen refused", []string{"--origin", "https://n.example", "--listen", "0.0.0.0:7070"}, "not a loopback address"},
		{"all interfaces refused", []string{"--origin", "https://n.example", "--listen", ":7070"}, "not a loopback address"},
		{"public listen allowed", []string{"--origin", "https://n.example", "--listen", "0.0.0.0:7070", "--allow-public-listen"}, ""},
		{"ipv6 loopback", []string{"--origin", "https://n.example", "--listen", "[::1]:7070"}, ""},
		{"stray args", []string{"--origin", "https://n.example", "extra"}, "unexpected arguments"},
	} {
		o, err := parseFlags(tc.args)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want it to contain %q (opts %+v)", tc.name, err, tc.wantErr, o)
		}
	}

	o, err := parseFlags([]string{"--origin", "https://n.example", "--socket", "/run/nyttig/nyttig.sock", "--tls-ca", "ca.pem"})
	if err != nil {
		t.Fatal(err)
	}
	if o.listen != "127.0.0.1:7070" || o.client.Addr != "/run/nyttig/nyttig.sock" || o.client.TLSCA != "ca.pem" {
		t.Errorf("parsed options = %+v", o)
	}
}
