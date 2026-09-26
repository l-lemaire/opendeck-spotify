package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg, err := LoadFrom(path)
	if err != nil || cfg.ClientID != "" || cfg.PluginDebug {
		t.Fatalf("missing file should load empty: %+v, %v", cfg, err)
	}
	cfg.ClientID = "abc123"
	cfg.PluginDebug = true
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("permissions %o", info.Mode().Perm())
	}
	again, err := LoadFrom(path)
	if err != nil || again.ClientID != "abc123" || !again.PluginDebug {
		t.Errorf("round trip = %+v, %v", again, err)
	}
	os.WriteFile(path, []byte("nope"), 0o600)
	if _, err := LoadFrom(path); err == nil {
		t.Error("corrupt file should fail")
	}
}
