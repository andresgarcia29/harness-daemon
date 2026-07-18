package initflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// La sonda real usa cap.Config.Command del catálogo embebido (inmutable), así
// que aquí se prueba la MECÁNICA: tier degradable, allowlist de claves,
// no-persistencia sin sonda ok, y la deny-list de tools.

func TestCapabilityTierYSeleccion(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	wsConRepos(t, m)
	runStepOK(t, m, "discover")

	// tier no degradable hacia arriba
	if _, code := m.Handle("capability", map[string]any{"name": "serena", "enabled": true, "tier": "destructive"}); code != 400 {
		t.Fatal("subir tier debe rechazarse")
	}
	if r, code := m.Handle("capability", map[string]any{"name": "serena", "enabled": true, "tier": "read-only"}); code != 200 {
		t.Fatalf("degradar tier vale: %v", r)
	}
	found := false
	for _, c := range m.Public().Answers.Capabilities {
		if c.Name == "serena" && c.Tier == "read-only" && c.Mcp == "serena" {
			found = true
		}
	}
	if !found {
		t.Fatalf("serena debió quedar en answers: %+v", m.Public().Answers.Capabilities)
	}
	// desactivar la quita
	if _, code := m.Handle("capability", map[string]any{"name": "serena", "enabled": false}); code != 200 {
		t.Fatal("disable")
	}
	for _, c := range m.Public().Answers.Capabilities {
		if c.Name == "serena" {
			t.Fatal("desactivada debe salir de answers")
		}
	}
	// capacidad desconocida
	if _, code := m.Handle("capability", map[string]any{"name": "yolo", "enabled": true}); code != 400 {
		t.Fatal("desconocida")
	}
}

func TestMcpSecretAllowlistYPersistencia(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	ws := wsConRepos(t, m)
	runStepOK(t, m, "discover")

	// clave que no pertenece al MCP → 400
	if _, code := m.Handle("mcp-secret", map[string]any{"name": "github-mcp", "key": "OTRA_COSA", "value": "x"}); code != 400 {
		t.Fatal("clave fuera del catálogo")
	}
	// el probe real fallará (docker/stub) → NO persiste
	if _, code := m.Handle("mcp-secret", map[string]any{"name": "github-mcp", "key": "GITHUB_PERSONAL_ACCESS_TOKEN", "value": "bad"}); code != 400 {
		t.Fatal("sonda fallida no persiste")
	}
	if _, err := os.Stat(filepath.Join(ws, ".secrets")); err == nil {
		t.Fatal("sin sonda ok no hay .secrets")
	}
	// el estado jamás contiene el valor
	b, _ := json.Marshal(m.Public())
	if strings.Contains(string(b), "bad") {
		t.Fatal("el valor del secreto no puede viajar en el estado")
	}
}

func TestUpsertSecretsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".secrets")
	if err := upsertSecretsFile(p, "A", "1"); err != nil {
		t.Fatal(err)
	}
	if err := upsertSecretsFile(p, "B", "2"); err != nil {
		t.Fatal(err)
	}
	if err := upsertSecretsFile(p, "A", "3"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Count(s, "A=") != 1 || !strings.Contains(s, "A=3") || !strings.Contains(s, "B=2") {
		t.Fatalf("upsert: %q", s)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf(".secrets debe ser 0600: %v", fi.Mode().Perm())
	}
}

func TestRunMcpsAplicaDeny(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	ws := wsConRepos(t, m)
	runStepOK(t, m, "discover")
	runStepOK(t, m, "generate")

	// elegir serena con tools acotadas + sonda simulada en el estado
	if _, code := m.Handle("capability", map[string]any{"name": "serena", "enabled": true,
		"tools_allowed": []any{"find_symbol"}}); code != 200 {
		t.Fatal("capability")
	}
	m.mu.Lock()
	m.st.McpProbes = map[string]McpProbeState{"serena": {OK: true, Tools: []string{"find_symbol", "replace_symbol", "execute_shell"}}}
	m.mu.Unlock()
	runStepOK(t, m, "mcps")

	b, err := os.ReadFile(filepath.Join(ws, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("settings.json debe seguir parseando: %v", err)
	}
	s := string(b)
	for _, want := range []string{"mcp__serena__replace_symbol", "mcp__serena__execute_shell"} {
		if !strings.Contains(s, want) {
			t.Fatalf("falta deny %s", want)
		}
	}
	if strings.Contains(s, "mcp__serena__find_symbol") {
		t.Fatal("la tool permitida no va en deny")
	}
	if !strings.Contains(s, "kubectl apply") {
		t.Fatal("los denies base del template deben conservarse")
	}
	// re-correr es idempotente (los mcp__ previos se limpian, no se duplican)
	runStepOK(t, m, "mcps")
	b2, _ := os.ReadFile(filepath.Join(ws, ".claude", "settings.json"))
	if strings.Count(string(b2), "mcp__serena__replace_symbol") != 1 {
		t.Fatal("re-aplicar no debe duplicar denies")
	}
	// .mcp.json lleva serena
	mcp, _ := os.ReadFile(filepath.Join(ws, ".mcp.json"))
	if !strings.Contains(string(mcp), "serena") {
		t.Fatalf(".mcp.json: %s", mcp)
	}
}
