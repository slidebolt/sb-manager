package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRuntimeConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, []byte(`{"components":{"plugin":{"environment":{"TOKEN":"secret"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Components["plugin"].Environment["TOKEN"]; got != "secret" {
		t.Fatalf("TOKEN = %q", got)
	}
}
