package version

import (
	"runtime/debug"
	"testing"
)

func TestString(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = "v1.2.3"
	if got := String(); got != "v1.2.3" {
		t.Errorf("String() with Version set = %q, want v1.2.3", got)
	}

	Version = ""
	if got := String(); got == "" {
		t.Error("String() without Version is empty")
	}
}

func TestFromBuildInfo(t *testing.T) {
	info := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}
	}
	tests := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info", nil, false, "dev"},
		{"empty version", info(""), true, "dev"},
		{"devel", info("(devel)"), true, "dev"},
		{"tag", info("v0.1.0"), true, "v0.1.0"},
		{"pseudo-version", info("v0.0.0-20260930141800-c52141fa7b2e+dirty"), true, "v0.0.0-20260930141800-c52141fa7b2e+dirty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromBuildInfo(tt.info, tt.ok); got != tt.want {
				t.Errorf("fromBuildInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}
