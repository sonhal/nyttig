// Package version reports which build of nyttig is running.
package version

import "runtime/debug"

// Version is the release version, set by CI for tagged builds:
//
//	go build -ldflags "-X github.com/sonhal/nyttig/internal/version.Version=v0.1.0" ./cmd/...
//
// It is empty in other builds; String then falls back to the build info.
var Version = ""

// String returns the version of the running binary: Version when it was set
// at build time, otherwise the module version go build stamped from the git
// checkout (a pseudo-version such as v0.0.0-20260930141800-c52141fa7b2e,
// with +dirty for uncommitted changes), otherwise "dev".
func String() string {
	if Version != "" {
		return Version
	}
	return fromBuildInfo(debug.ReadBuildInfo())
}

func fromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
