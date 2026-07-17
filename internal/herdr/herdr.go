// Package herdr lee el estado vivo de herdr (el multiplexor de agentes) por su
// CLI/socket. Es la capa de EJECUCIÓN del plan: herdr sostiene los PTYs reales
// de cualquier agente (Claude Code, OpenCode, Codex, Kimi…), persiste al
// detach, y corre local / por SSH / en el pod. El daemon lo LEE — así ves todo
// lo que corre en una máquina, no solo lo que este daemon lanzó.
//
// Verificado contra herdr 0.7.3: `herdr api snapshot` (workspaces/tabs/panes +
// agent_status) y `herdr pane read <id> --source visible` (terminal en vivo).
package herdr

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/redact"
)

type Pane struct {
	PaneID        string `json:"pane_id"`
	WorkspaceID   string `json:"workspace_id"`
	TabID         string `json:"tab_id"`
	Cwd           string `json:"cwd"`
	ForegroundCwd string `json:"foreground_cwd"`
	AgentStatus   string `json:"agent_status"`
	Focused       bool   `json:"focused"`
	Program       string `json:"program,omitempty"` // "Claude Code" | "Kimi" | … detectado
}
type Tab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	AgentStatus string `json:"agent_status"`
	PaneCount   int    `json:"pane_count"`
}
type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
	AgentStatus string `json:"agent_status"`
	PaneCount   int    `json:"pane_count"`
	TabCount    int    `json:"tab_count"`
	Focused     bool   `json:"focused"`
}
type Agent struct {
	Name        string `json:"name"`
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	AgentStatus string `json:"agent_status"`
	Cwd         string `json:"cwd"`
}

// State es lo que /api/herdr devuelve. Available=false → herdr no está (el
// panel esconde la vista con gracia). Cross-workspace a propósito.
type State struct {
	Available  bool        `json:"available"`
	Version    string      `json:"version"`
	Reason     string      `json:"reason,omitempty"`
	Workspaces []Workspace `json:"workspaces"`
	Tabs       []Tab       `json:"tabs"`
	Panes      []Pane      `json:"panes"`
	Agents     []Agent     `json:"agents"`
}

func run(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, "herdr", args...).Output()
}

// Snapshot lee el estado vivo. Fail-open: si herdr no está o el server no
// corre, devuelve Available:false con la razón — jamás tumba al daemon.
func Snapshot() State {
	if _, err := exec.LookPath("herdr"); err != nil {
		return State{Available: false, Reason: "herdr no está instalado en esta máquina",
			Workspaces: []Workspace{}, Panes: []Pane{}, Tabs: []Tab{}, Agents: []Agent{}}
	}
	out, err := run(4*time.Second, "api", "snapshot")
	if err != nil {
		return State{Workspaces: []Workspace{}, Panes: []Pane{}, Tabs: []Tab{}, Agents: []Agent{},
			Reason: "el server de herdr no está corriendo — lanza `herdr` para arrancarlo"}
	}
	return parse(out)
}

// parse traduce la respuesta de `herdr api snapshot` a nuestro State.
func parse(out []byte) State {
	st := State{Workspaces: []Workspace{}, Panes: []Pane{}, Tabs: []Tab{}, Agents: []Agent{}}
	var env struct {
		Result struct {
			Snapshot struct {
				Version    string      `json:"version"`
				Workspaces []Workspace `json:"workspaces"`
				Tabs       []Tab       `json:"tabs"`
				Panes      []Pane      `json:"panes"`
				Agents     []Agent     `json:"agents"`
			} `json:"snapshot"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &env) != nil {
		st.Reason = "no pude leer el snapshot de herdr (¿versión incompatible?)"
		return st
	}
	s := env.Result.Snapshot
	st.Available = true
	st.Version = s.Version
	if s.Workspaces != nil {
		st.Workspaces = s.Workspaces
	}
	if s.Tabs != nil {
		st.Tabs = s.Tabs
	}
	if s.Panes != nil {
		st.Panes = s.Panes
	}
	if s.Agents != nil {
		st.Agents = s.Agents
	}
	enrichPrograms(st.Panes)
	return st
}

// ── Detección del programa que corre (Claude Code / Kimi / …) ────────────
var (
	progMu    sync.Mutex
	progCache = map[string]progEntry{}
)

type progEntry struct {
	prog string
	at   time.Time
}

// enrichPrograms llena Pane.Program vía process-info, SOLO para panes con un
// agente detectado (status != unknown). Cache de 15 s por pane, en paralelo:
// no infla el snapshot.
func enrichPrograms(panes []Pane) {
	var wg sync.WaitGroup
	for i := range panes {
		if panes[i].AgentStatus == "" || panes[i].AgentStatus == "unknown" {
			continue
		}
		id := panes[i].PaneID
		progMu.Lock()
		e, ok := progCache[id]
		progMu.Unlock()
		if ok && time.Since(e.at) < 15*time.Second {
			panes[i].Program = e.prog
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			prog := detectProgram(id)
			progMu.Lock()
			progCache[id] = progEntry{prog, time.Now()}
			progMu.Unlock()
			panes[idx].Program = prog
		}(i)
	}
	wg.Wait()
}

var agentPrograms = []struct{ needle, label string }{
	{"claude", "Claude Code"}, {"kimi", "Kimi"}, {"codex", "Codex"},
	{"opencode", "OpenCode"}, {"cursor", "Cursor"}, {"aider", "Aider"},
	{"gemini", "Gemini"}, {"vertex", "Vertex"}, {"amp", "Amp"}, {"copilot", "Copilot"},
}

func detectProgram(paneID string) string {
	out, err := run(3*time.Second, "pane", "process-info", "--pane", paneID)
	if err != nil {
		return ""
	}
	var env struct {
		Result struct {
			Info struct {
				Foreground []struct {
					Argv0   string `json:"argv0"`
					Name    string `json:"name"`
					Cmdline string `json:"cmdline"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &env) != nil {
		return ""
	}
	for _, p := range env.Result.Info.Foreground {
		hay := strings.ToLower(p.Argv0 + " " + p.Name + " " + p.Cmdline)
		for _, ap := range agentPrograms {
			if strings.Contains(hay, ap.needle) {
				return ap.label
			}
		}
	}
	return ""
}

// SessionStop detiene una sesión ENTERA de herdr (cierra todas sus terminales).
func SessionStop(name string) error {
	if name == "" {
		name = "default"
	}
	_, err := run(6*time.Second, "session", "stop", name)
	return err
}

// PaneRead devuelve el terminal EN VIVO de un pane (redactado — una terminal
// puede tener un token en pantalla). --source visible = lo que se ve ahora.
// format "ansi" conserva los colores SGR (el frontend los renderiza).
func PaneRead(paneID string, lines int, format string) (string, error) {
	if paneID == "" {
		return "", nil
	}
	if lines <= 0 || lines > 200 {
		lines = 60
	}
	if format != "ansi" {
		format = "text"
	}
	out, err := run(4*time.Second, "pane", "read", paneID,
		"--source", "visible", "--lines", itoa(lines), "--format", format)
	if err != nil {
		return "", err
	}
	return redact.String(string(out)), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// PaneSend escribe texto + Enter en un pane (herdr pane run) — así se contesta
// a un agente interactivo. El caller DEBE validar pane_id contra el snapshot.
func PaneSend(paneID, text string) error {
	_, err := run(5*time.Second, "pane", "run", paneID, text)
	return err
}

// ── Control de ciclo de vida (destructivo — el caller valida el id) ───────

// Interrupt manda Ctrl-C a un pane (send-keys C-c) — corta el proceso sin
// cerrar la terminal.
func Interrupt(paneID string) error {
	_, err := run(5*time.Second, "pane", "send-keys", paneID, "C-c")
	return err
}

// ClosePane cierra un pane (mata su terminal).
func ClosePane(paneID string) error {
	_, err := run(5*time.Second, "pane", "close", paneID)
	return err
}

// CloseTab cierra un tab (y sus panes).
func CloseTab(tabID string) error {
	_, err := run(5*time.Second, "tab", "close", tabID)
	return err
}

// CloseWorkspace cierra un workspace entero.
func CloseWorkspace(wsID string) error {
	_, err := run(5*time.Second, "workspace", "close", wsID)
	return err
}

// ── Teclas (respuestas interactivas de menú) ─────────────────────────────

// PaneKeys manda teclas literales a un pane (herdr pane send-keys). Para
// contestar un menú de agente: la tecla del número, o "Enter"/"y"/"n". El
// caller valida el pane contra el snapshot.
func PaneKeys(paneID string, keys []string) error {
	if paneID == "" || len(keys) == 0 {
		return nil
	}
	args := append([]string{"pane", "send-keys", paneID}, keys...)
	_, err := run(5*time.Second, args...)
	return err
}

// ── Abrir (crear) — devuelven el id nuevo ────────────────────────────────

func idFrom(out []byte, path ...string) string {
	var m map[string]any
	if json.Unmarshal(out, &m) != nil {
		return ""
	}
	cur := any(m)
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

// WorkspaceCreate abre un workspace nuevo (grupo aislado de tabs/panes).
func WorkspaceCreate(label, cwd string) (string, error) {
	args := []string{"workspace", "create", "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	out, err := run(6*time.Second, args...)
	if err != nil {
		return "", err
	}
	return idFrom(out, "result", "workspace", "workspace_id"), nil
}

// TabCreate abre una terminal nueva (tab) en un workspace.
func TabCreate(wsID, label, cwd string) (string, error) {
	args := []string{"tab", "create", "--no-focus"}
	if wsID != "" {
		args = append(args, "--workspace", wsID)
	}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	out, err := run(6*time.Second, args...)
	if err != nil {
		return "", err
	}
	return idFrom(out, "result", "tab", "tab_id"), nil
}

// PaneSplit divide un pane (terminal lado a lado). dir: right | down.
func PaneSplit(paneID, dir, cwd string) (string, error) {
	if dir != "right" && dir != "down" {
		dir = "down"
	}
	args := []string{"pane", "split", paneID, "--direction", dir, "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	out, err := run(6*time.Second, args...)
	if err != nil {
		return "", err
	}
	return idFrom(out, "result", "pane", "pane_id"), nil
}
