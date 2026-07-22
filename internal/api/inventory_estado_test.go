package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// La lección del panel de Andres: el estado de MCPs y la procedencia de
// skills deben VERSE siempre. Protege: llaves faltantes por MCP (nombres,
// jamás valores) y la capa de cada skill por su marca en disco.
func TestEstadoMcpYProcedenciaSkills(t *testing.T) {
	ws := t.TempDir()

	// .mcp.json: github envuelto; context7 sin secretos
	mcp := map[string]any{"mcpServers": map[string]any{
		"github":   map[string]any{"command": "scripts/with-secrets.sh", "args": []string{"docker", "run"}},
		"context7": map[string]any{"command": "npx", "args": []string{"-y", "c7"}},
	}}
	b, _ := json.Marshal(mcp)
	os.WriteFile(filepath.Join(ws, ".mcp.json"), b, 0o644)
	// .secrets con UNA de las claves que el catálogo exige para github-mcp
	os.WriteFile(filepath.Join(ws, ".secrets"), []byte("OTRA=x\n"), 0o600)

	servers := BuildMcp(ws)
	var gh, c7 *McpServer
	for i := range servers {
		switch servers[i].Name {
		case "github":
			gh = &servers[i]
		case "context7":
			c7 = &servers[i]
		}
	}
	if gh == nil || c7 == nil {
		t.Fatal("faltan servers en BuildMcp")
	}
	if len(gh.SecretsNeeded) == 0 {
		t.Fatal("github-mcp debe declarar claves desde el catálogo embebido")
	}
	if len(gh.SecretsMissing) != len(gh.SecretsNeeded) {
		t.Fatalf("todas las claves de github deberían faltar (hay OTRA): needed=%v missing=%v", gh.SecretsNeeded, gh.SecretsMissing)
	}
	if len(c7.SecretsNeeded) != 0 {
		t.Fatalf("context7 no pide secretos; needed=%v", c7.SecretsNeeded)
	}

	// skills: upstream (gen-manifest) + compartida (.managed) + local (nada)
	mk := func(name, extra string) {
		d := filepath.Join(ws, ".claude", "skills", name)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: d\n---\n"), 0o644)
		if extra != "" {
			os.WriteFile(filepath.Join(d, ".managed"), []byte(extra+"\n"), 0o644)
		}
	}
	mk("skill-creator", "")
	mk("deploy-x", "repo.git@main#abc123")
	mk("mi-local", "")
	os.MkdirAll(filepath.Join(ws, ".harness"), 0o755)
	os.WriteFile(filepath.Join(ws, ".harness", "gen-manifest.json"),
		[]byte(`{".claude/skills/skill-creator/SKILL.md":"x"}`), 0o644)
	// BuildToolbox exige señales de harness
	os.MkdirAll(filepath.Join(ws, ".claude", "commands"), 0o755)
	os.WriteFile(filepath.Join(ws, ".claude", "commands", "auto.md"), []byte("---\ndescription: d\n---\n"), 0o644)

	tb := BuildToolbox(ws)
	if tb == nil {
		t.Fatal("toolbox nil")
	}
	got := map[string][2]string{}
	for _, sk := range tb.Skills {
		got[sk.Name] = [2]string{sk.Layer, sk.Source}
	}
	if got["skill-creator"][0] != "upstream" {
		t.Fatalf("skill-creator: layer=%q, quiero upstream", got["skill-creator"][0])
	}
	if got["deploy-x"][0] != "compartida" || got["deploy-x"][1] != "repo.git@main#abc123" {
		t.Fatalf("deploy-x: %v, quiero compartida con fuente", got["deploy-x"])
	}
	if got["mi-local"][0] != "local" {
		t.Fatalf("mi-local: layer=%q, quiero local", got["mi-local"][0])
	}
}
