package app

import "testing"

func TestConfigCarriesBinDir(t *testing.T) {
	cfg := Config{BinDir: ".bin"}
	if cfg.BinDir != ".bin" {
		t.Fatalf("binDir: got %q want %q", cfg.BinDir, ".bin")
	}
}
