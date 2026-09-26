// Package config persists the non-secret state of the CLI and plugin.
// Secrets (Spotify tokens) live in package secrets, never here.
//
// The file is JSON at $XDG_CONFIG_HOME/spotify-cli/config.json, which on
// Linux resolves to ~/.config/spotify-cli/config.json. It is safe to read,
// share and edit by hand.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the whole file.
type Config struct {
	Version int `json:"version"`
	// ClientID is the Spotify developer application id used for the web
	// API. It is public by design (it appears in browser URLs) and is
	// useless without the tokens stored in the keyring.
	ClientID string `json:"client_id,omitempty"`
	// PluginDebug makes the OpenDeck plugin write full debug output
	// (protocol messages, D-Bus and HTTP exchanges) to its log file.
	PluginDebug bool `json:"plugin_debug,omitempty"`

	path string // where it was loaded from; not serialised
}

// Dir returns the directory holding config.json and the credentials
// fallback file. It honours $XDG_CONFIG_HOME through os.UserConfigDir.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "spotify-cli"), nil
}

// PluginLogPath returns the plugin's log file under $XDG_STATE_HOME
// (default ~/.local/state).
func PluginLogPath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "opendeck-spotify", "plugin.log"), nil
}

// Load reads the config file. A missing file yields an empty, usable Config.
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return LoadFrom(filepath.Join(dir, "config.json"))
}

// LoadFrom is Load with an explicit path, for tests and unusual setups.
func LoadFrom(path string) (*Config, error) {
	cfg := &Config{Version: 1, path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("config %s is corrupt: %w", path, err)
	}
	return cfg, nil
}

// Path returns where Save will write.
func (c *Config) Path() string { return c.path }

// Save writes the file with owner-only permissions.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, append(raw, '\n'), 0o600)
}
