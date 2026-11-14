// Package config handles XDG paths and the user's TOML defaults.
package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config holds user defaults (all optional).
type Config struct {
	DataDir  string   `toml:"data_dir"`  // override of the xwechat_files directory
	Account  string   `toml:"account"`   // account folder name, if several exist
	TZ       string   `toml:"tz"`        // IANA zone for date/week bucketing; empty = local
	OutDir   string   `toml:"out_dir"`   // default export directory
	Formats  []string `toml:"formats"`   // json, csv, sqlite
	Raw      bool     `toml:"raw"`       // include raw_content column
	ExcelBOM bool     `toml:"excel_bom"` // UTF-8 BOM in CSV
}

// Dir returns $XDG_CONFIG_HOME/wxexport (or ~/.config/wxexport).
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "wxexport"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "wxexport"), nil
}

func ConfigPath() (string, error) { return join("config.toml") }
func KeysPath() (string, error)   { return join("keys.json") }

func join(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

// Load reads path; a missing file yields defaults.
func Load(path string) (Config, error) {
	c := Default()
	_, err := toml.DecodeFile(path, &c)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	return c, err
}

func Default() Config {
	return Config{Formats: []string{"json", "csv", "sqlite"}}
}

// Save writes the config atomically with mode 0600.
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RuntimeDir returns $XDG_RUNTIME_DIR or os.TempDir().
func RuntimeDir() string {
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		return x
	}
	return os.TempDir()
}
