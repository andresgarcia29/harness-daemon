package gen

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Inventory — el shape EXACTO que produce scripts/discover.sh (bash+jq).
// Cero juicio: señales deterministas por repo. Lo consumen el seeding de
// answers, el enrichment LLM y el filtro del catálogo.
type InvRepo struct {
	Name          string   `json:"name"`
	CurrentBranch string   `json:"current_branch"`
	Remote        string   `json:"remote"`
	RoleGuess     string   `json:"role_guess"`
	FileCount     int      `json:"file_count"`
	Languages     []string `json:"languages"`
	Signals       []string `json:"signals"`
	HasClaudeMD   bool     `json:"has_claude_md"`
}

type Inventory struct {
	Workspace   string              `json:"workspace"`
	ScannedAt   string              `json:"scanned_at"`
	RepoCount   int                 `json:"repo_count"`
	Repos       []InvRepo           `json:"repos"`
	SecretHints []string            `json:"secret_hints"`
	ByRole      map[string][]string `json:"by_role"`
	Summary     map[string][]string `json:"summary"`
}

// LoadInventory lee <ws>/inventory.json (el artefacto del paso discover).
func LoadInventory(ws string) (*Inventory, error) {
	b, err := os.ReadFile(filepath.Join(ws, "inventory.json"))
	if err != nil {
		return nil, err
	}
	var inv Inventory
	if err := json.Unmarshal(b, &inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

// RoleOf devuelve el rol EFECTIVO de un repo (override del humano > guess).
func (inv *Inventory) RoleOf(name string, overrides map[string]string) string {
	if r, ok := overrides[name]; ok && r != "" {
		return r
	}
	for _, r := range inv.Repos {
		if r.Name == name {
			return r.RoleGuess
		}
	}
	return "unknown"
}

// ValidRoles — los roles que discover.sh puede inferir (y el humano corregir).
var ValidRoles = []string{
	"service", "frontend", "mobile", "library", "contracts",
	"infra-module", "infra-live", "ci-library", "docs", "unknown",
}

func ValidRole(r string) bool {
	for _, v := range ValidRoles {
		if v == r {
			return true
		}
	}
	return false
}
