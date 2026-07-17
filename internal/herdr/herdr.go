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
	return st
}

// PaneRead devuelve el terminal EN VIVO de un pane (redactado — una terminal
// puede tener un token en pantalla). --source visible = lo que se ve ahora.
func PaneRead(paneID string, lines int) (string, error) {
	if paneID == "" {
		return "", nil
	}
	if lines <= 0 || lines > 200 {
		lines = 60
	}
	out, err := run(4*time.Second, "pane", "read", paneID,
		"--source", "visible", "--lines", itoa(lines), "--format", "text")
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
