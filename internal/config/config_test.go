package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingGivesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || len(c.Formats) != 3 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestSaveLoadRoundTripAndPerm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	in := Config{TZ: "Europe/Paris", OutDir: "/x", Formats: []string{"csv"}, Raw: true}
	if err := Save(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(p)
	if err != nil || out.TZ != in.TZ || out.OutDir != in.OutDir || !out.Raw || len(out.Formats) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("perm %v", fi.Mode().Perm())
	}
}

func TestXDGPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdgtest")
	if p, _ := KeysPath(); p != "/tmp/xdgtest/wxexport/keys.json" {
		t.Errorf("keys path %s", p)
	}
}
