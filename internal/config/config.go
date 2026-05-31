// Package config loads nyttigd's optional TOML configuration file.
//
// The config file supplies defaults for the daemon's runtime options
// (socket, db_path, log_level) and a declarative set of sources, tags, and
// tag rules that are seeded into the database on startup. See sample_config.toml
// for the expected format.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config mirrors the structure of the TOML config file.
type Config struct {
	Socket   string    `toml:"socket"`
	DBPath   string    `toml:"db_path"`
	LogLevel string    `toml:"log_level"`
	Sources  []Source  `toml:"sources"`
	Tags     []Tag     `toml:"tags"`
	TagRules []TagRule `toml:"tag_rules"`
}

// Source is a feed source declared in the config file.
type Source struct {
	Name       string `toml:"name"`
	URL        string `toml:"url"`
	RefreshSec int    `toml:"refresh_sec"`
	Type       string `toml:"type"`
}

// Tag is a tag declared in the config file.
type Tag struct {
	Name  string `toml:"name"`
	Color string `toml:"color"`
}

// TagRule is an auto-tagging rule declared in the config file. Source is the
// optional name of a source to scope the rule to; empty means a global rule.
type TagRule struct {
	Tag      string `toml:"tag"`
	Pattern  string `toml:"pattern"`
	Field    string `toml:"field"`
	Source   string `toml:"source"`
	Priority int    `toml:"priority"`
}

// Load reads and parses the TOML config file at path. Unknown keys are
// reported as an error so typos in the config don't silently no-op.
func Load(path string) (*Config, error) {
	var cfg Config
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("unknown config keys in %s: %v", path, undecoded)
	}
	return &cfg, nil
}

// ExpandHome expands a leading "~" in path to the user's home directory.
func ExpandHome(path string) string {
	if path == "~" || len(path) >= 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
