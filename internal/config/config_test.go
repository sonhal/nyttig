package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoad_SampleConfig keeps sample_config.toml in sync with the Config
// struct: Load rejects unknown keys, so a renamed or removed key fails here.
func TestLoad_SampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "sample_config.toml"))
	if err != nil {
		t.Fatalf("Load(sample_config.toml): %v", err)
	}
	if len(cfg.Sources) == 0 {
		t.Error("sample config should declare sources")
	}
}

func TestLoad_BlockPrivateAddresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("block_private_addresses = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.BlockPrivateAddresses {
		t.Error("block_private_addresses = true was not loaded")
	}
}

func TestLoad_RejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("block_private_adresses = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for a misspelled key")
	}
}
