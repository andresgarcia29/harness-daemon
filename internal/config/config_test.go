package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAusente(t *testing.T) {
	t.Setenv("HARNESS_CONFIG_DIR", t.TempDir())
	if c := Load(); c.UIPort != 0 {
		t.Fatalf("config ausente debe dar zero value, dio %+v", c)
	}
	if p := ResolveUIPort(0); p != DefaultUIPort {
		t.Fatalf("sin flag ni config debe resolver %d, dio %d", DefaultUIPort, p)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	t.Setenv("HARNESS_CONFIG_DIR", t.TempDir())
	if err := Save(Config{UIPort: 7999}); err != nil {
		t.Fatal(err)
	}
	if c := Load(); c.UIPort != 7999 {
		t.Fatalf("roundtrip: %+v", c)
	}
	fi, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("config.json debe ser 0600, es %v", fi.Mode().Perm())
	}
}

func TestPrecedencia(t *testing.T) {
	t.Setenv("HARNESS_CONFIG_DIR", t.TempDir())
	if err := Save(Config{UIPort: 7999}); err != nil {
		t.Fatal(err)
	}
	if p := ResolveUIPort(8123); p != 8123 {
		t.Fatalf("el flag explícito gana: %d", p)
	}
	if p := ResolveUIPort(0); p != 7999 {
		t.Fatalf("sin flag gana el config: %d", p)
	}
}

func TestLoadCorrupto(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HARNESS_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{no es json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := ResolveUIPort(0); p != DefaultUIPort {
		t.Fatalf("config corrupto degrada al default, dio %d", p)
	}
}
