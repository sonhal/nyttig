package main

import (
	"reflect"
	"testing"
)

func TestPlanListeners(t *testing.T) {
	tests := []struct {
		name          string
		socket        string
		tlsListen     string
		tlsConfigured bool
		want          []listenerSpec
		wantErr       bool
	}{
		{
			name:   "plaintext unix socket",
			socket: "/run/nyttig/nyttig.sock",
			want:   []listenerSpec{{addr: "/run/nyttig/nyttig.sock"}},
		},
		{
			name:          "tls on the socket address",
			socket:        ":9090",
			tlsConfigured: true,
			want:          []listenerSpec{{addr: ":9090", tls: true}},
		},
		{
			name:          "unix socket plus mtls port",
			socket:        "/run/nyttig/nyttig.sock",
			tlsListen:     ":9090",
			tlsConfigured: true,
			want: []listenerSpec{
				{addr: "/run/nyttig/nyttig.sock"},
				{addr: ":9090", tls: true},
			},
		},
		{
			name:      "tls listen without certificates",
			socket:    "/run/nyttig/nyttig.sock",
			tlsListen: ":9090",
			wantErr:   true,
		},
		{
			// Would serve a plaintext TCP port next to the mTLS one.
			name:          "tls listen with a tcp socket",
			socket:        "127.0.0.1:9091",
			tlsListen:     ":9090",
			tlsConfigured: true,
			wantErr:       true,
		},
		{
			name:          "tls listen that is not a tcp address",
			socket:        "/run/nyttig/nyttig.sock",
			tlsListen:     "/run/nyttig/tls.sock",
			tlsConfigured: true,
			wantErr:       true,
		},
		{
			name:    "empty socket",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := planListeners(tt.socket, tt.tlsListen, tt.tlsConfigured)
			if (err != nil) != tt.wantErr {
				t.Fatalf("planListeners() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("planListeners() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
