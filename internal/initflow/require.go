package initflow

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ── paso requirements: las dependencias del wizard y del harness ──
// Determinista: LookPath + `--version` con timeout. Instalar solo ejecuta
// comandos DEL CATÁLOGO/baseline (jamás strings del navegador), solo brew en
// macOS; en Linux se devuelve el comando para correrlo con sudo a mano.

type ReqState struct {
	Name       string `json:"name"`
	Bin        string `json:"bin"`
	OK         bool   `json:"ok"`
	Version    string `json:"version,omitempty"`
	Installing bool   `json:"installing,omitempty"`
	Install    string `json:"install,omitempty"` // comando/instrucción para ESTE os
	AutoRun    bool   `json:"auto_run,omitempty"`
	NeedsSudo  bool   `json:"needs_sudo,omitempty"`
	Optional   bool   `json:"optional,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	Error      string `json:"error,omitempty"`
}

type reqDef struct {
	Name, Bin, Install, Purpose string
	Optional                    bool
}

// baseline: lo que el WIZARD necesita (las capacidades elegidas se instalan
// después, en el bootstrap de la instancia — ahí ya hay make init).
var baseline = []reqDef{
	{Name: "git", Bin: "git", Install: "brew install git | apt-get install -y git", Purpose: "clonar y versionar — todo el harness vive en git"},
	{Name: "jq", Bin: "jq", Install: "brew install jq | apt-get install -y jq", Purpose: "discover.sh y doctor.sh parsean JSON con jq"},
	{Name: "claude", Bin: "claude", Install: "npm install -g @anthropic-ai/claude-code", Purpose: "los pasos LLM (enrichment, arqueología) y el pipeline /auto"},
	{Name: "gh", Bin: "gh", Install: "brew install gh | apt-get install -y gh", Purpose: "GitHub CLI — opcional si conectaste con PAT", Optional: true},
	{Name: "herdr", Bin: "herdr", Install: "https://github.com/andresgarcia29/herdr", Purpose: "multiplexor de terminales del panel — opcional", Optional: true},
}

// installFor elige el comando de un spec "brew … | apt-get … | url" para este
// OS. autoRun solo para brew en darwin (sin sudo, sin sorpresas).
func installFor(spec, goos string) (cmd string, autoRun, needsSudo bool) {
	for _, part := range strings.Split(spec, "|") {
		p := strings.TrimSpace(part)
		switch {
		case goos == "darwin" && strings.HasPrefix(p, "brew "):
			return p, true, false
		case goos == "linux" && (strings.HasPrefix(p, "apt-get ") || strings.HasPrefix(p, "apt ")):
			return "sudo " + p, false, true
		}
	}
	// sin match por OS: la primera alternativa como instrucción manual
	first := strings.TrimSpace(strings.Split(spec, "|")[0])
	return first, false, false
}

func checkReq(d reqDef) ReqState {
	r := ReqState{Name: d.Name, Bin: d.Bin, Optional: d.Optional, Purpose: d.Purpose}
	r.Install, r.AutoRun, r.NeedsSudo = installFor(d.Install, runtime.GOOS)
	path, err := exec.LookPath(d.Bin)
	if err != nil {
		return r
	}
	r.OK = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, path, "--version").Output(); err == nil {
		v := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		if len(v) > 60 {
			v = v[:60]
		}
		r.Version = v
	}
	return r
}

// CheckBaseline — el check standalone (lo usa el Manager local y
// `harness init-step requirements` corriendo en un VPS).
func CheckBaseline() []ReqState {
	out := make([]ReqState, 0, len(baseline))
	for _, d := range baseline {
		out = append(out, checkReq(d))
	}
	return out
}

func (m *Manager) refreshRequirements() []ReqState {
	out := CheckBaseline()
	m.mu.Lock()
	m.st.Requirements = out
	m.persistLocked()
	m.mu.Unlock()
	return out
}

// runRequirements — el runner del paso: verde solo si TODO lo requerido está.
func (m *Manager) runRequirements() error {
	if m.isRemote() {
		return m.remoteRequirements()
	}
	reqs := m.refreshRequirements()
	var missing []string
	for _, r := range reqs {
		if !r.OK && !r.Optional {
			missing = append(missing, r.Name)
		}
		m.logs.Append("requirements", reqLine(r))
	}
	if len(missing) > 0 {
		return fmt.Errorf("faltan: %s — instálalos (botón o a mano) y reintenta", strings.Join(missing, ", "))
	}
	return nil
}

func reqLine(r ReqState) string {
	switch {
	case r.OK:
		return "✓ " + r.Name + " — " + r.Version
	case r.Optional:
		return "○ " + r.Name + " (opcional) no está — " + r.Install
	default:
		return "✗ " + r.Name + " FALTA — " + r.Install
	}
}

// handleRequirementsCheck: re-verifica y devuelve (para la checklist viva).
func (m *Manager) handleRequirementsCheck(map[string]any) (any, int) {
	if m.isRemote() {
		// en remoto el check vive en el runner (paso 4); la checklist viva
		// del snapshot se refresca al correrlo
		if err := m.remoteRequirements(); err != nil {
			m.mu.Lock()
			reqs := append([]ReqState(nil), m.st.Requirements...)
			m.mu.Unlock()
			return map[string]any{"ok": true, "requirements": reqs, "note": err.Error()}, 200
		}
		m.mu.Lock()
		reqs := append([]ReqState(nil), m.st.Requirements...)
		m.mu.Unlock()
		return map[string]any{"ok": true, "requirements": reqs}, 200
	}
	return map[string]any{"ok": true, "requirements": m.refreshRequirements()}, 200
}

// handleInstall: instala UNA dependencia del baseline por nombre. El comando
// sale de nuestro spec, jamás del navegador. Solo auto-run (brew/darwin);
// lo demás vuelve como instrucción con needs_sudo.
func (m *Manager) handleInstall(body map[string]any) (any, int) {
	if m.isRemote() {
		return map[string]any{"ok": false, "manual": true,
			"error": "en instalación remota las dependencias se instalan en el VPS a mano (sudo)"}, 200
	}
	name := str(body, "name")
	var def *reqDef
	for i := range baseline {
		if baseline[i].Name == name {
			def = &baseline[i]
		}
	}
	if def == nil {
		return map[string]any{"ok": false, "error": "dependencia desconocida: " + name}, 400
	}
	cmd, autoRun, needsSudo := installFor(def.Install, runtime.GOOS)
	if !autoRun {
		return map[string]any{"ok": false, "manual": true, "command": cmd, "needs_sudo": needsSudo,
			"error": "esta instalación se corre a mano: " + cmd}, 200
	}
	m.mu.Lock()
	if m.installing {
		m.mu.Unlock()
		return map[string]any{"ok": false, "error": "ya hay una instalación corriendo"}, 409
	}
	m.installing = true
	m.mu.Unlock()
	m.markInstalling(name, true)
	go func() {
		defer func() {
			m.mu.Lock()
			m.installing = false
			m.mu.Unlock()
			m.markInstalling(name, false)
			m.refreshRequirements()
		}()
		m.logs.Append("requirements", "$ "+cmd)
		parts := strings.Fields(cmd)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		c := exec.CommandContext(ctx, parts[0], parts[1:]...)
		out, err := c.CombinedOutput()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) != "" {
				m.logs.Append("requirements", line)
			}
		}
		if err != nil {
			m.logs.Append("requirements", "❌ instalación de "+name+" falló: "+err.Error())
		} else {
			m.logs.Append("requirements", "✓ "+name+" instalado")
		}
	}()
	return map[string]any{"ok": true, "installing": name}, 200
}

func (m *Manager) markInstalling(name string, v bool) {
	m.mu.Lock()
	for i := range m.st.Requirements {
		if m.st.Requirements[i].Name == name {
			m.st.Requirements[i].Installing = v
		}
	}
	m.persistLocked()
	m.mu.Unlock()
}
