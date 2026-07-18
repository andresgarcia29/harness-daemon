package initflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/api"
	"github.com/andresgarcia29/harness-daemon/internal/gen"
)

// ── paso mcps: el catálogo, secretos certificados por sonda, y tools ──
// Leyes: el navegador solo manda NOMBRES del catálogo; el tier solo se
// DEGRADA; un secreto se sonda ANTES de persistirse (certificado = el MCP
// contestó el handshake con ese secreto puesto); el valor jamás vuelve.

type McpProbeState struct {
	OK    bool     `json:"ok"`
	Ms    int64    `json:"ms"`
	Tools []string `json:"tools,omitempty"`
	Error string   `json:"error,omitempty"`
}

var tierRank = map[string]int{"read-only": 0, "read-write": 1, "destructive": 2}

// handleCatalog — el menú del paso: MCPs del catálogo con su estado.
func (m *Manager) handleCatalog(map[string]any) (any, int) {
	caps, err := gen.Catalog()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 500
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	chosen := map[string]gen.CapSel{}
	if m.st.Answers != nil {
		for _, c := range m.st.Answers.Capabilities {
			chosen[c.Name] = c
		}
	}
	type item struct {
		Name       string          `json:"name"`
		Category   string          `json:"category"`
		Purpose    string          `json:"purpose"`
		Mcp        string          `json:"mcp"`
		Tier       string          `json:"tier"`
		Phase      int             `json:"phase,omitempty"`
		Secrets    []gen.SecretRef `json:"secrets,omitempty"`
		Evidence   string          `json:"evidence,omitempty"`
		Enabled    bool            `json:"enabled"`
		ChosenTier string          `json:"chosen_tier,omitempty"`
		Tools      []string        `json:"tools,omitempty"`
		ToolsAllowed []string      `json:"tools_allowed,omitempty"`
		SecretsOK  map[string]bool `json:"secrets_ok,omitempty"`
		Probe      *McpProbeState  `json:"probe,omitempty"`
	}
	var out []item
	for _, c := range caps {
		if c.Provider != "mcp" {
			continue
		}
		it := item{Name: c.Name, Category: c.Category, Purpose: c.Purpose, Mcp: c.Mcp,
			Tier: c.PermissionTier, Phase: c.Phase, Secrets: c.Secrets,
			Evidence: m.st.Recommendations["capability:"+c.Name]}
		if sel, ok := chosen[c.Name]; ok {
			it.Enabled = true
			it.ChosenTier = sel.Tier
			it.ToolsAllowed = sel.ToolsAllowed
		}
		if p, ok := m.st.McpProbes[c.Name]; ok {
			p := p
			it.Probe = &p
			it.Tools = p.Tools
		}
		if len(c.Secrets) > 0 {
			it.SecretsOK = map[string]bool{}
			for _, s := range c.Secrets {
				it.SecretsOK[s.Key] = m.st.SecretKeys[s.Key]
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		ei, ej := out[i].Evidence != "", out[j].Evidence != ""
		if ei != ej {
			return ei
		}
		return out[i].Name < out[j].Name
	})
	return map[string]any{"ok": true, "catalog": out}, 200
}

// handleCapability — activar/desactivar una capacidad, degradar tier, acotar tools.
func (m *Manager) handleCapability(body map[string]any) (any, int) {
	name := str(body, "name")
	cap, ok := gen.CapByName(name)
	if !ok {
		return map[string]any{"ok": false, "error": "capacidad desconocida: " + name}, 400
	}
	tier := str(body, "tier")
	if tier == "" {
		tier = cap.PermissionTier
	}
	if tierRank[tier] > tierRank[cap.PermissionTier] {
		return map[string]any{"ok": false, "error": "el tier solo se degrada (catálogo: " + cap.PermissionTier + ")"}, 400
	}
	var toolsAllowed []string
	if raw, isArr := body["tools_allowed"].([]any); isArr {
		for _, t := range raw {
			if s, ok := t.(string); ok && s != "" {
				toolsAllowed = append(toolsAllowed, s)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Answers == nil {
		return map[string]any{"ok": false, "error": "aún no hay configuración — corre el discover"}, 409
	}
	caps := m.st.Answers.Capabilities[:0]
	for _, c := range m.st.Answers.Capabilities {
		if c.Name != name {
			caps = append(caps, c)
		}
	}
	if boolv(body, "enabled") {
		scope := "core"
		if cap.Cronjob != "" {
			scope = "cronjob"
		}
		caps = append(caps, gen.CapSel{Name: cap.Name, Bin: cap.Bin, Mcp: cap.Mcp,
			Tier: tier, Scope: scope, Profiles: cap.Profiles, ToolsAllowed: toolsAllowed})
	}
	sort.Slice(caps, func(i, j int) bool { return caps[i].Name < caps[j].Name })
	m.st.Answers.Capabilities = caps
	m.st.AnswersRev++
	m.persistLocked()
	return map[string]any{"ok": true, "rev": m.st.AnswersRev}, 200
}

// handleMcpSecret — {name, key, value}: sonda con el secreto inyectado ANTES
// de persistir. Solo claves del catálogo. Con fuente env el valor va a
// <ws>/.secrets (0600); con otras fuentes solo se registra la referencia
// (el valor lo materializa secrets.sh desde tu fuente).
func (m *Manager) handleMcpSecret(body map[string]any) (any, int) {
	name, key, value := str(body, "name"), str(body, "key"), str(body, "value")
	cap, ok := gen.CapByName(name)
	if !ok || cap.Config == nil {
		return map[string]any{"ok": false, "error": "MCP desconocido: " + name}, 400
	}
	allowed := false
	for _, s := range cap.Secrets {
		if s.Key == key {
			allowed = true
		}
	}
	if !allowed {
		return map[string]any{"ok": false, "error": "la clave " + key + " no pertenece a " + name}, 400
	}
	if value == "" {
		return map[string]any{"ok": false, "error": "valor vacío"}, 400
	}
	m.mu.Lock()
	ws := m.st.Workspace
	source := "env"
	slug := ""
	if m.st.Answers != nil {
		source = m.st.Answers.Secrets.Source
		slug = m.st.Answers.Project.Name
	}
	m.mu.Unlock()
	if ws == "" {
		return map[string]any{"ok": false, "error": "workspace no fijado"}, 409
	}

	// certificar: el MCP debe contestar el handshake con el secreto puesto
	args := make([]string, 0, len(cap.Config.Args))
	for _, a := range cap.Config.Args {
		args = append(args, strings.ReplaceAll(a, "{{PROJECT_SLUG}}", slug))
	}
	probe := api.ProbeMcpCommand(ws, cap.Config.Command, args, map[string]string{key: value})
	m.mu.Lock()
	if m.st.McpProbes == nil {
		m.st.McpProbes = map[string]McpProbeState{}
	}
	m.st.McpProbes[name] = McpProbeState{OK: probe.OK, Ms: probe.Ms, Tools: probe.Tools, Error: probe.Error}
	m.mu.Unlock()
	if !probe.OK {
		m.logs.Append("mcps", "❌ "+name+": la sonda no contestó con ese secreto ("+probe.Error+")")
		return map[string]any{"ok": false, "verified": false,
			"error": "el MCP no contestó con ese secreto: " + probe.Error}, 400
	}

	stored := false
	if source == "env" {
		if err := upsertSecretsFile(filepath.Join(ws, ".secrets"), key, value); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, 500
		}
		stored = true
	}
	m.mu.Lock()
	if m.st.SecretKeys == nil {
		m.st.SecretKeys = map[string]bool{}
	}
	m.st.SecretKeys[key] = stored
	if !stored && m.st.Answers != nil { // fuente externa: registra la referencia
		ref := source + "://" + key
		dup := false
		for _, r := range m.st.Answers.Secrets.Refs {
			if r == ref {
				dup = true
			}
		}
		if !dup {
			m.st.Answers.Secrets.Refs = append(m.st.Answers.Secrets.Refs, ref)
			m.st.AnswersRev++
		}
	}
	m.persistLocked()
	m.mu.Unlock()
	m.logs.Append("mcps", "✓ "+name+" certificado ("+key+", "+fmt.Sprint(probe.Ms)+"ms, "+fmt.Sprint(len(probe.Tools))+" tools)")
	note := ""
	if !stored {
		note = "fuente " + source + ": el valor NO se guardó aquí — regístralo en tu fuente; secrets.sh lo materializa"
	}
	return map[string]any{"ok": true, "verified": true, "stored": stored, "note": note, "tools": probe.Tools}, 200
}

// handleProbeInit — sondar un MCP sin secreto (los que no lo necesitan).
func (m *Manager) handleProbeInit(body map[string]any) (any, int) {
	name := str(body, "name")
	cap, ok := gen.CapByName(name)
	if !ok || cap.Config == nil {
		return map[string]any{"ok": false, "error": "MCP desconocido: " + name}, 400
	}
	m.mu.Lock()
	ws := m.st.Workspace
	slug := ""
	if m.st.Answers != nil {
		slug = m.st.Answers.Project.Name
	}
	m.mu.Unlock()
	args := make([]string, 0, len(cap.Config.Args))
	for _, a := range cap.Config.Args {
		args = append(args, strings.ReplaceAll(a, "{{PROJECT_SLUG}}", slug))
	}
	probe := api.ProbeMcpCommand(ws, cap.Config.Command, args, nil)
	m.mu.Lock()
	if m.st.McpProbes == nil {
		m.st.McpProbes = map[string]McpProbeState{}
	}
	m.st.McpProbes[name] = McpProbeState{OK: probe.OK, Ms: probe.Ms, Tools: probe.Tools, Error: probe.Error}
	m.persistLocked()
	m.mu.Unlock()
	return map[string]any{"ok": probe.OK, "tools": probe.Tools, "ms": probe.Ms, "error": probe.Error}, 200
}

// runMcps — materializa la selección: regenera (.mcp.json + answers.yaml,
// idempotente) y aplica la deny-list de tools a settings.json.
func (m *Manager) runMcps() error {
	m.mu.Lock()
	ws := m.st.Workspace
	a := m.st.Answers
	inv := m.inv
	probes := map[string][]string{}
	for name, p := range m.st.McpProbes {
		probes[name] = p.Tools
	}
	m.mu.Unlock()
	if a == nil || inv == nil {
		return fmt.Errorf("configuración incompleta — corre discover/generate primero")
	}
	rep, err := gen.Generate(a, inv, gen.Opts{WS: ws, Version: m.version, Now: time.Now()})
	if err != nil {
		return err
	}
	m.logs.Append("mcps", fmt.Sprintf("selección materializada (.mcp.json + answers) — %d actualizados", rep.Updated+rep.Created))
	if err := gen.ApplyToolDeny(ws, a, probes); err != nil {
		return err
	}
	m.logs.Append("mcps", "deny-list de tools aplicada en .claude/settings.json")
	return nil
}

// upsertSecretsFile — KEY=VALUE en .secrets, 0600, reemplaza si existe.
func upsertSecretsFile(path, key, value string) error {
	lines := []string{}
	if b, err := os.ReadFile(path); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l != "" && !strings.HasPrefix(l, key+"=") {
				lines = append(lines, l)
			}
		}
	}
	lines = append(lines, key+"="+value)
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
