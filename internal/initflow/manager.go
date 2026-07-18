package initflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
	"github.com/andresgarcia29/harness-daemon/internal/ident"
)

// Manager orquesta el wizard. Un daemon tiene a lo sumo UN manager (el init es
// singular por diseño: un workspace naciendo a la vez por proceso).
type Manager struct {
	mu      sync.Mutex
	st      State
	adopt   func(path string) error // fija el workspace del daemon (late binding, una vez)
	logs       *LogBuffer
	version    string
	running    bool // a lo sumo un paso corriendo
	installing bool // a lo sumo una instalación de dependencia corriendo

	// resolveTarget valida el NOMBRE de un target SSH (main lo inyecta con
	// api.ResolveTarget para no crear ciclo de imports). nil = sin targets.
	resolveTarget func(name string) (string, bool)

	inv *gen.Inventory // inventario cargado (artefacto <ws>/inventory.json)
}

// SetTargetResolver inyecta el validador de targets (api.ResolveTarget).
func (m *Manager) SetTargetResolver(f func(string) (string, bool)) { m.resolveTarget = f }

// New crea el manager en modo setup. Si hay un init a medias (de un arranque
// anterior), lo carga y re-adopta su workspace: reanudar es el caso normal,
// no el excepcional.
func New(version string, adopt func(string) error) *Manager {
	m := &Manager{version: version, adopt: adopt, logs: NewLogBuffer()}
	m.st = loadState()
	if !m.st.Active {
		m.st = State{Version: schemaVersion, HarnessVersion: version, Active: true,
			Current: "workspace", Steps: freshSteps()}
		m.persistLocked()
	}
	if m.st.Workspace != "" {
		if err := adopt(m.st.Workspace); err != nil {
			m.setStepLocked("workspace", Fail, "", "no pude re-adoptar el workspace: "+err.Error())
		}
		m.loadInventory()
	}
	return m
}

// loadInventory recarga el artefacto del discover si existe (resume).
func (m *Manager) loadInventory() {
	m.mu.Lock()
	ws := m.st.Workspace
	m.mu.Unlock()
	if ws == "" {
		return
	}
	if inv, err := gen.LoadInventory(ws); err == nil {
		m.mu.Lock()
		m.inv = inv
		m.mu.Unlock()
	}
}

// Attach carga un init a medias dentro de un workspace ya adoptado (daemon
// arrancado con --workspace normal). nil si no hay init activo ahí.
func Attach(version, ws string, adopt func(string) error) *Manager {
	st, ok := readState(wsStatePath(ws))
	if !ok || !st.Active {
		return nil
	}
	m := &Manager{version: version, adopt: adopt, logs: NewLogBuffer(), st: st}
	m.loadInventory()
	return m
}

// ── persistencia ──
// El estado canónico vive en <ws>/.harness/init/state.json en cuanto hay
// workspace; ConfigDir()/init/state.json es el arranque y el puntero de resume.

func cfgStatePath() string  { return filepath.Join(ident.ConfigDir(), "init", "state.json") }
func wsStatePath(ws string) string {
	return filepath.Join(ws, ".harness", "init", "state.json")
}

func readState(path string) (State, bool) {
	var st State
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &st) != nil || st.Version != schemaVersion {
		return State{}, false
	}
	return st, true
}

func loadState() State {
	cfg, ok := readState(cfgStatePath())
	if !ok {
		return State{}
	}
	if cfg.Workspace != "" {
		if ws, ok := readState(wsStatePath(cfg.Workspace)); ok {
			return ws // el canónico es el del workspace
		}
	}
	return cfg
}

func writeState(path string, st State) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (m *Manager) persistLocked() {
	if m.st.Workspace != "" {
		writeState(wsStatePath(m.st.Workspace), m.st)
	}
	// ConfigDir siempre lleva al menos el puntero (resume tras reinicio).
	_ = os.MkdirAll(filepath.Join(ident.ConfigDir(), "init"), 0o700)
	writeState(cfgStatePath(), m.st)
}

// ── vistas ──

func (m *Manager) Public() *PublicState {
	m.mu.Lock()
	defer m.mu.Unlock()
	steps := make([]StepState, len(m.st.Steps))
	copy(steps, m.st.Steps)
	for i := range steps {
		steps[i].LogsTail = m.logs.Tail(steps[i].ID, tailN)
	}
	repos := make([]RepoSel, len(m.st.Repos))
	copy(repos, m.st.Repos)
	reqs := make([]ReqState, len(m.st.Requirements))
	copy(reqs, m.st.Requirements)
	return &PublicState{
		Active: m.st.Active, Step: m.st.Current, Steps: steps,
		WorkspacePath: m.st.Workspace, Target: m.st.Target,
		GitHub: m.st.GitHub, Repos: repos, Requirements: reqs,
		Inventory: m.inv, Answers: m.st.Answers, AnswersRev: m.st.AnswersRev,
		RoleOverrides: m.st.RoleOverrides, Recommendations: m.st.Recommendations,
		CompletedAt: m.st.CompletedAt,
	}
}

func (m *Manager) Logs() *LogBuffer { return m.logs }

// WorkspacePath — para el arranque (resume) y los tests.
func (m *Manager) WorkspacePath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st.Workspace
}

// ── helpers de estado ──

func (m *Manager) stepIdx(id string) int {
	for i := range m.st.Steps {
		if m.st.Steps[i].ID == id {
			return i
		}
	}
	return -1
}

func (m *Manager) setStepLocked(id string, s Status, detail, errMsg string) {
	if i := m.stepIdx(id); i >= 0 {
		m.st.Steps[i].Status = s
		m.st.Steps[i].Detail = detail
		m.st.Steps[i].Error = errMsg
	}
	m.advanceLocked()
	m.persistLocked()
}

// advanceLocked: el paso actual es el primero que no está resuelto.
func (m *Manager) advanceLocked() {
	for _, st := range m.st.Steps {
		if st.Status != OK && st.Status != Skipped {
			m.st.Current = st.ID
			return
		}
	}
	m.st.Current = "finish"
}

// ── el dispatcher HTTP (main.go pone el Guard; aquí solo lógica) ──

// Handle procesa una acción del wizard y devuelve (respuesta, código HTTP).
func (m *Manager) Handle(action string, body map[string]any) (any, int) {
	switch action {
	case "workspace":
		return m.handleWorkspace(body)
	case "browse":
		return m.handleBrowse(body)
	case "target":
		return m.handleTarget(body)
	case "github":
		return m.handleGithub(body)
	case "github-orgs":
		return m.handleGithubOrgs(body)
	case "github-repos":
		return m.handleGithubRepos(body)
	case "repo-tags":
		return m.handleRepoTags(body)
	case "repos":
		return m.handleRepoSelect(body)
	case "requirements-check":
		return m.handleRequirementsCheck(body)
	case "install":
		return m.handleInstall(body)
	case "role":
		return m.handleRole(body)
	case "answers":
		return m.handleAnswers(body)
	case "answers-confirm":
		return m.handleAnswersConfirm(body)
	case "step":
		return m.handleStep(body)
	default:
		return map[string]any{"ok": false, "error": "acción desconocida: " + action}, 400
	}
}

// handleBrowse: mini file-browser del paso 1. SOLO directorios, SOLO bajo el
// home (el navegador no tiene por qué ver más), symlinks sin seguir, ocultos
// fuera. La UI no implementa seguridad — la respeta.
func (m *Manager) handleBrowse(body map[string]any) (any, int) {
	p := str(body, "path")
	home, _ := os.UserHomeDir()
	if p == "" {
		p = home
	}
	norm, err := normalizeWS(p, false)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 400
	}
	entries, err := os.ReadDir(norm)
	if err != nil {
		return map[string]any{"ok": false, "error": "no pude leer la carpeta: " + err.Error()}, 400
	}
	dirs := []string{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	parent := filepath.Dir(norm)
	if norm == home {
		parent = "" // el techo del browse es el home
	}
	return map[string]any{"ok": true, "path": norm, "parent": parent, "dirs": dirs}, 200
}

// handleTarget: dónde se crea el harness — "" (esta máquina) o el NOMBRE de
// un target SSH de targets.json (la instalación remota corre los pasos allá,
// F11). El navegador manda el nombre, jamás el SSH: anti-inyección de targets.go.
func (m *Manager) handleTarget(body map[string]any) (any, int) {
	name := str(body, "name")
	if name != "" && m.resolveTarget != nil {
		if _, ok := m.resolveTarget(name); !ok {
			return map[string]any{"ok": false, "error": "target desconocido: " + name}, 400
		}
	}
	m.mu.Lock()
	m.st.Target = name
	m.persistLocked()
	m.mu.Unlock()
	return map[string]any{"ok": true, "target": name}, 200
}

func str(b map[string]any, k string) string {
	v, _ := b[k].(string)
	return strings.TrimSpace(v)
}
func boolv(b map[string]any, k string) bool {
	v, _ := b[k].(bool)
	return v
}

// intv acepta float64 (JSON) e int (tests/uso interno).
func intv(b map[string]any, k string) int {
	switch v := b[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// ── paso workspace ──

func normalizeWS(p string, allowOutsideHome bool) (string, error) {
	if p == "" {
		return "", fmt.Errorf("ruta vacía")
	}
	if strings.Contains(p, "..") {
		return "", fmt.Errorf("la ruta no puede contener «..»")
	}
	home, _ := os.UserHomeDir()
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home == "" {
			return "", fmt.Errorf("no sé dónde está tu home")
		}
		p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("la ruta debe ser absoluta (o empezar con ~/)")
	}
	p = filepath.Clean(p)
	if !allowOutsideHome && home != "" && p != home && !strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "", fmt.Errorf("fuera de tu home (%s) — si es a propósito, confirma con confirm_outside_home", home)
	}
	return p, nil
}

func probeDir(p string) (exists, writable, empty bool) {
	fi, err := os.Stat(p)
	if err != nil || !fi.IsDir() {
		// escribible = ¿puedo crearla? (el ancestro existente debe dejarme)
		return false, ancestorWritable(p), true
	}
	exists = true
	entries, _ := os.ReadDir(p)
	empty = len(entries) == 0
	probe := filepath.Join(p, ".harness-probe")
	if f, err := os.Create(probe); err == nil {
		f.Close()
		_ = os.Remove(probe)
		writable = true
	}
	return exists, writable, empty
}

func ancestorWritable(p string) bool {
	for d := filepath.Dir(p); ; d = filepath.Dir(d) {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			probe := filepath.Join(d, ".harness-probe")
			if f, err := os.Create(probe); err == nil {
				f.Close()
				_ = os.Remove(probe)
				return true
			}
			return false
		}
		if d == filepath.Dir(d) {
			return false
		}
	}
}

func (m *Manager) handleWorkspace(body map[string]any) (any, int) {
	norm, err := normalizeWS(str(body, "path"), boolv(body, "confirm_outside_home"))
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 400
	}
	exists, writable, empty := probeDir(norm)
	if boolv(body, "dry_run") {
		return map[string]any{"ok": true, "normalized": norm,
			"exists": exists, "writable": writable, "empty": empty}, 200
	}

	m.mu.Lock()
	if m.st.Workspace == norm { // idempotente: el refresh re-manda el paso 1
		m.mu.Unlock()
		return map[string]any{"ok": true, "workspace": norm, "created": false}, 200
	}
	if m.st.Workspace != "" {
		cur := m.st.Workspace
		m.mu.Unlock()
		return map[string]any{"ok": false,
			"error": "este init ya está anclado a " + cur + " — termina o borra ese init primero"}, 409
	}
	m.mu.Unlock()

	created := false
	if !exists {
		if !boolv(body, "create") {
			return map[string]any{"ok": false, "error": "la carpeta no existe — manda create:true para crearla", "normalized": norm}, 400
		}
		if err := os.MkdirAll(norm, 0o755); err != nil {
			return map[string]any{"ok": false, "error": "no pude crearla: " + err.Error()}, 500
		}
		created = true
	} else if !writable {
		return map[string]any{"ok": false, "error": "la carpeta existe pero no puedo escribir en ella"}, 400
	}
	if err := os.MkdirAll(filepath.Join(norm, "repos"), 0o755); err != nil {
		return map[string]any{"ok": false, "error": "no pude crear repos/: " + err.Error()}, 500
	}
	if err := m.adopt(norm); err != nil {
		return map[string]any{"ok": false, "error": "no pude adoptar el workspace: " + err.Error()}, 500
	}

	m.mu.Lock()
	m.st.Workspace = norm
	m.logs.Append("workspace", "workspace fijado en "+norm)
	m.setStepLocked("workspace", OK, norm, "")
	m.mu.Unlock()
	return map[string]any{"ok": true, "workspace": norm, "created": created}, 200
}

// ── paso genérico: run / retry / skip ──

func (m *Manager) handleStep(body map[string]any) (any, int) {
	id, action := str(body, "step"), str(body, "action")
	d, ok := defOf(id)
	if !ok {
		return map[string]any{"ok": false, "error": "paso desconocido: " + id}, 400
	}
	switch action {
	case "skip":
		if !d.Skippable {
			return map[string]any{"ok": false, "error": "el paso «" + d.Title + "» no se puede saltar"}, 400
		}
		m.mu.Lock()
		if i := m.stepIdx(id); i >= 0 && m.st.Steps[i].Status == Running {
			m.mu.Unlock()
			return map[string]any{"ok": false, "error": "el paso está corriendo"}, 409
		}
		m.logs.Append(id, "paso saltado por el usuario")
		m.setStepLocked(id, Skipped, "saltado", "")
		m.mu.Unlock()
		return map[string]any{"ok": true, "step": id, "status": Skipped}, 200
	case "run", "retry", "":
		r := m.runnerFor(id)
		if r == nil {
			return map[string]any{"ok": false, "error": "el paso «" + d.Title + "» aún no está implementado en esta versión"}, 400
		}
		m.mu.Lock()
		if m.running {
			m.mu.Unlock()
			return map[string]any{"ok": false, "error": "ya hay un paso corriendo — espera a que termine"}, 409
		}
		if m.st.Workspace == "" && id != "workspace" {
			m.mu.Unlock()
			return map[string]any{"ok": false, "error": "workspace no fijado — completa el paso 1"}, 409
		}
		m.running = true
		if i := m.stepIdx(id); i >= 0 {
			m.st.Steps[i].Status = Running
			m.st.Steps[i].Error = ""
		}
		m.persistLocked()
		m.mu.Unlock()
		go func() {
			err := r(m)
			m.mu.Lock()
			m.running = false
			if err != nil {
				m.logs.Append(id, "❌ "+err.Error())
				m.setStepLocked(id, Fail, "", err.Error())
			} else {
				m.setStepLocked(id, OK, "", "")
			}
			m.mu.Unlock()
		}()
		return map[string]any{"ok": true, "step": id, "status": Running}, 200
	default:
		return map[string]any{"ok": false, "error": "acción de paso desconocida: " + action}, 400
	}
}
