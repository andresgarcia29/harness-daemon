// Package config guarda las preferencias del usuario que sobreviven entre
// arranques y que NO son secretos: hoy, el puerto del panel. Vive en
// ConfigDir()/config.json — misma carpeta que los tokens, pero este archivo
// es legible (0600 igualmente: costumbre de la casa).
//
// El puerto canónico del panel es 7180 (ADR-0011). Precedencia:
// flag explícito > config.json > default.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/andresgarcia29/harness-daemon/internal/ident"
)

// DefaultUIPort es el puerto canónico de `harness init` / `harness ui`.
// 7718 queda como default legacy de `harness daemon run` (ADR-0005/0011).
const DefaultUIPort = 7180

type Config struct {
	UIPort int `json:"ui_port,omitempty"`
}

func Path() string { return filepath.Join(ident.ConfigDir(), "config.json") }

// Load lee el config; ausente o corrupto → zero value (fail-open: unas
// preferencias ilegibles jamás impiden arrancar el panel).
func Load() Config {
	var c Config
	b, err := os.ReadFile(Path())
	if err != nil {
		return c
	}
	_ = json.Unmarshal(b, &c)
	return c
}

// Save escribe atómico (tmp+rename), creando ConfigDir si hace falta.
func Save(c Config) error {
	dir := ident.ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

// ResolveUIPort aplica la precedencia flag > config > default. Un flag en 0
// significa "no especificado" (nadie pide el puerto 0 a propósito).
func ResolveUIPort(flagPort int) int {
	if flagPort > 0 {
		return flagPort
	}
	if c := Load(); c.UIPort > 0 {
		return c.UIPort
	}
	return DefaultUIPort
}
