// Package gen contiene los assets embebidos del instalador (templates,
// catálogo, scripts) y —en fases siguientes— el generador determinista.
//
// Los assets los sincroniza scripts/sync-assets.sh desde harness-installer;
// assets/manifest.json registra el commit de origen y el sha256 de cada
// archivo. CI corre `sync-assets.sh --check`: el drift es un fallo, no una
// sorpresa embebida en un release.
package gen

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed assets
var assetsFS embed.FS

// Asset lee un archivo embebido por su ruta relativa (p.ej. "scripts/discover.sh").
func Asset(path string) ([]byte, error) { return assetsFS.ReadFile("assets/" + path) }

// Manifest — de dónde salieron los assets.
type Manifest struct {
	InstallerCommit string            `json:"installer_commit"`
	Files           map[string]string `json:"files"`
}

func LoadManifest() (Manifest, error) {
	var m Manifest
	b, err := Asset("manifest.json")
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// ── el catálogo de capacidades (el menú de la entrevista) ──

type SecretRef struct {
	Key    string `json:"key"`
	Source string `json:"source"`
}
type CapConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}
type Capability struct {
	Name           string      `json:"name"`
	Category       string      `json:"category"`
	Purpose        string      `json:"purpose"`
	Provider       string      `json:"provider"` // cli | mcp | script
	Bin            string      `json:"bin,omitempty"`
	Mcp            string      `json:"mcp,omitempty"`
	Config         *CapConfig  `json:"config,omitempty"`
	Wrap           bool        `json:"wrap,omitempty"`
	Install        string      `json:"install,omitempty"`
	PermissionTier string      `json:"permission_tier,omitempty"`
	Profiles       []string    `json:"profiles,omitempty"`
	Detect         string      `json:"detect,omitempty"`
	Secrets        []SecretRef `json:"secrets,omitempty"`
	Phase          int         `json:"phase,omitempty"`
	Cronjob        string      `json:"cronjob,omitempty"`
	Note           string      `json:"note,omitempty"`
}

var (
	catOnce sync.Once
	catList []Capability
	catErr  error
)

// Catalog devuelve las capacidades embebidas (parseadas una vez).
func Catalog() ([]Capability, error) {
	catOnce.Do(func() {
		b, err := Asset("catalog/capabilities.json")
		if err != nil {
			catErr = err
			return
		}
		var doc struct {
			Capabilities []Capability `json:"capabilities"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			catErr = fmt.Errorf("catálogo embebido corrupto: %w", err)
			return
		}
		catList = doc.Capabilities
	})
	return catList, catErr
}

// CapByName busca una capacidad por nombre exacto.
func CapByName(name string) (Capability, bool) {
	caps, err := Catalog()
	if err != nil {
		return Capability{}, false
	}
	for _, c := range caps {
		if c.Name == name {
			return c, true
		}
	}
	return Capability{}, false
}
