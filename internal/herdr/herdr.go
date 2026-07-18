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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/redact"
)

type Pane struct {
	PaneID        string      `json:"pane_id"`
	WorkspaceID   string      `json:"workspace_id"`
	TabID         string      `json:"tab_id"`
	Cwd           string      `json:"cwd"`
	ForegroundCwd string      `json:"foreground_cwd"`
	AgentStatus   string      `json:"agent_status"`
	Focused       bool        `json:"focused"`
	Program       string      `json:"program,omitempty"`       // "Claude Code" | "Kimi" | … detectado
	Agent         string      `json:"agent,omitempty"`         // "claude" | "codex" | … (de herdr)
	AgentSession  *SessionRef `json:"agent_session,omitempty"` // id de la sesión del agente → su transcripción
}

// SessionRef es el ancla a la transcripción del agente (el JSONL de Claude Code
// vive por este id). Nos deja mostrar la conversación COMPLETA, no la pantalla.
type SessionRef struct {
	Agent string `json:"agent,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Value string `json:"value,omitempty"`
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

// State es lo que /api/herdr devuelve. Available=false → el server de herdr no
// corre (el panel esconde la vista con gracia). Installed distingue "no está el
// binario" de "está pero el server no corre" — en el 2º caso ofrecemos activarlo.
// Cross-workspace a propósito.
type State struct {
	Available  bool        `json:"available"` // el server corre y respondió el snapshot
	Installed  bool        `json:"installed"` // el binario herdr está en el PATH
	Stale      bool        `json:"stale,omitempty"` // remoto: SSH no respondió, mostrando lo último
	Version    string      `json:"version"`
	Reason     string      `json:"reason,omitempty"`
	Workspaces []Workspace `json:"workspaces"`
	Tabs       []Tab       `json:"tabs"`
	Panes      []Pane      `json:"panes"`
	Agents     []Agent     `json:"agents"`
	Sessions   []Session   `json:"sessions"` // sesiones de herdr (viven aunque el server esté parado)
}

// Session es una sesión de herdr (el multiplexor persistente). Es lo que
// mantiene VIVAS las terminales: si running=false quedó como registro histórico
// y se puede borrar. Se lee con `herdr session list`, que responde aunque el
// server no esté corriendo.
type Session struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Default bool   `json:"default"`
	Dir     string `json:"dir,omitempty"`
}

// Client apunta el adapter a UNA máquina. target vacío = local (corre `herdr`
// directo). target no vacío = un destino SSH (alias de ~/.ssh/config o
// user@host): corre `ssh <target> herdr …` — así el panel local ve y opera el
// herdr de un VPS. La autenticación la maneja OpenSSH (llaves/agente): el daemon
// NUNCA toca credenciales. BatchMode=yes → si no hay llave, falla rápido, no
// cuelga pidiendo password.
type Client struct{ target string }

// Remote devuelve un Client hacia un destino ("" = local). El caller DEBE haber
// validado el target contra la lista de destinos guardados (ver api/targets).
func Remote(target string) Client { return Client{target: strings.TrimSpace(target)} }

// Local es el cliente de esta máquina (retrocompatibilidad).
func Local() Client { return Client{} }

// controlDir es un directorio PROPIO 0700 para los sockets de ControlMaster —
// NO /tmp (world-writable): en un host compartido un co-tenant podría pre-crear
// el socket en una ruta predecible y secuestrar la conexión multiplexada. Con
// un dir 0700 sólo nuestro usuario escribe ahí. Cae a os.TempDir si no hay HOME.
func controlDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return os.TempDir()
	}
	d := filepath.Join(home, ".ssh", "harness-cm")
	_ = os.MkdirAll(d, 0o700)
	return d
}

// sshBase son las opciones de ssh: sin password, timeout corto, y ControlMaster
// para reusar UNA conexión autenticada (el snapshot corre cada 2s — sin esto
// pagaríamos un handshake SSH cada vez). ControlPath usa %C (hash opaco de la
// conexión): nombre impredecible y corto (evita el límite ~104 del socket unix).
func sshBase() []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=6",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + filepath.Join(controlDir(), "%C"),
		"-o", "ControlPersist=30s",
	}
}

// shQuote envuelve un argumento en comillas simples POSIX (escapando las '),
// para que el shell REMOTO no interprete nada (`;`, `$()`, espacios). Es el
// blindaje contra inyección al mandar texto/teclas a un herdr remoto.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shQuote(a)
	}
	return strings.Join(parts, " ")
}

// remotePATH antepone las ubicaciones comunes de instalación al PATH remoto.
// `ssh host cmd` corre en un shell NO-interactivo con un PATH mínimo
// (/usr/bin:/bin) que casi nunca incluye donde vive herdr (Homebrew, ~/.local/
// bin…): por eso `ssh host herdr` falla aunque interactivamente sí lo halles.
// $HOME y $PATH los expande el shell remoto; el resto es literal y seguro.
const remotePATH = `PATH="$HOME/.local/bin:$HOME/bin:/opt/homebrew/bin:/usr/local/bin:/home/linuxbrew/.linuxbrew/bin:/snap/bin:$PATH" `

// run ejecuta un subcomando de herdr en el destino del Client. Local = exec
// directo (args como argv, sin shell). Remoto = ssh con el comando ya quoteado
// y el PATH aumentado.
func (c Client) run(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if c.target == "" {
		return exec.CommandContext(ctx, "herdr", args...).Output()
	}
	remote := remotePATH + "herdr " + shJoin(args)
	sshArgs := append(sshBase(), c.target, remote)
	out, err := exec.CommandContext(ctx, "ssh", sshArgs...).Output()
	if err != nil {
		// el stderr de ssh trae la causa real (Permission denied, Connection
		// refused, herdr: not found…); lo pegamos al error para no perderlo.
		if ee, okk := err.(*exec.ExitError); okk && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
	}
	return out, err
}

// Snapshot lee el estado vivo. Fail-open: si herdr no está o el server no
// corre, devuelve Available:false con la razón — jamás tumba al daemon. Las
// sesiones se leen siempre (aunque el server esté parado) para poder verlas,
// pararlas, borrarlas y ofrecer "activar herdr".
// Snapshot local (retrocompatibilidad).
func Snapshot() State { return Client{}.Snapshot() }

// Snapshot lee el estado vivo del destino del Client. Fail-open. Distingue tres
// casos: no instalado, instalado pero server parado, y (remoto) no alcanzable
// por SSH — cada uno con su razón, para que el panel ofrezca lo correcto.
func (c Client) Snapshot() State {
	// Dedupe de frescura (sólo remoto): si varios paneles/SSE ven el mismo VPS,
	// comparten un fetch reciente en vez de cada uno lanzar su tormenta de SSH.
	// Ventana corta (<intervalo SSE) para que un solo cliente siga fresco.
	if c.target != "" {
		if st, ok := snapRecent(c.target, 800*time.Millisecond); ok {
			return st
		}
	}
	empty := func() State {
		return State{Workspaces: []Workspace{}, Panes: []Pane{}, Tabs: []Tab{}, Agents: []Agent{}, Sessions: []Session{}}
	}
	// Local: sabemos de inmediato si falta el binario. Remoto: lo inferimos de
	// si herdr responde por SSH (no podemos LookPath en la otra máquina).
	if c.target == "" {
		if _, err := exec.LookPath("herdr"); err != nil {
			st := empty()
			st.Reason = "herdr no está instalado en esta máquina"
			return st
		}
	}
	// Las dos lecturas son independientes; en remoto cada una es un round-trip
	// SSH, así que en paralelo reducimos la latencia a la mitad.
	type sres struct {
		ss  []Session
		err error
	}
	type ares struct {
		out []byte
		err error
	}
	sch, ach := make(chan sres, 1), make(chan ares, 1)
	go func() { ss, e := c.sessionList(); sch <- sres{ss, e} }()
	go func() { o, e := c.run(4*time.Second, "api", "snapshot"); ach <- ares{o, e} }()
	sr, ar := <-sch, <-ach
	sessions, slErr := sr.ss, sr.err
	out, err := ar.out, ar.err
	if err != nil {
		// Remoto que dejó de responder: en vez de blanquear, mostramos su último
		// estado bueno reciente (marcado Stale). Sólo si el fallo parece de red
		// (session list TAMBIÉN falló); si session list respondió, el server
		// simplemente está parado y eso sí lo reportamos tal cual.
		if c.target != "" && slErr != nil {
			if last, ok := snapRecent(c.target, 12*time.Second); ok && last.Available {
				last.Stale = true
				last.Reason = "sin respuesta de «" + c.target + "» — reintentando (mostrando lo último)"
				return last
			}
		}
		st := empty()
		st.Sessions = sessions
		switch {
		case c.target == "":
			st.Installed = true
			st.Reason = "el server de herdr no está corriendo — actívalo para ver tus terminales en vivo"
		case slErr == nil:
			// herdr respondió `session list` por SSH → está instalado y alcanzable
			st.Installed = true
			st.Reason = "el server de herdr en «" + c.target + "» no está corriendo — actívalo"
		default:
			st.Reason = "no pude leer herdr en «" + c.target + "» por SSH (¿herdr instalado allá? ¿tu llave lista?)"
		}
		return st
	}
	st := c.parse(out)
	st.Installed = true
	st.Sessions = sessions
	if c.target != "" {
		snapRemember(c.target, st)
	}
	return st
}

// Probe es un chequeo barato del estado de un destino (para dots de conexión y
// el botón "Probar"). Distingue tres casos que importan al onboarding: SSH no
// conecta, SSH conecta pero herdr no está, y herdr responde (parado o corriendo).
type Probe struct {
	Reachable bool   `json:"reachable"` // herdr respondió por SSH
	SSHOK     bool   `json:"ssh_ok"`    // la conexión SSH funciona
	Running   bool   `json:"running"`   // el server de herdr corre
	Message   string `json:"message"`
}

// Probe hace un chequeo ligero (una llamada; dos sólo si la primera falla).
func (c Client) Probe() Probe {
	ss, err := c.sessionList()
	if err == nil {
		p := Probe{Reachable: true, SSHOK: true}
		for _, s := range ss {
			if s.Running {
				p.Running = true
			}
		}
		if p.Running {
			p.Message = "herdr corriendo"
		} else {
			p.Message = "herdr instalado, server parado — puedes activarlo"
		}
		return p
	}
	// herdr no respondió: ¿es SSH o es que falta herdr en el VPS?
	if c.sshReachable() {
		return Probe{SSHOK: true, Message: "conecté por SSH, pero herdr no respondió — ¿está instalado en el VPS?"}
	}
	return Probe{Message: "no pude conectar por SSH — revisa el host y que tu llave entre sin contraseña"}
}

// sshReachable prueba SOLO la conexión SSH (sin herdr): `ssh <target> true`.
func (c Client) sshReachable() bool {
	if c.target == "" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	args := append(sshBase(), c.target, "true")
	return exec.CommandContext(ctx, "ssh", args...).Run() == nil
}

// sessionList lee `herdr session list --json`. Responde aunque el server esté
// parado (por eso una sesión "stopped" sigue apareciendo hasta que se borra).
// Devuelve error para distinguir "no alcanzable" de "sin sesiones".
func (c Client) sessionList() ([]Session, error) {
	out, err := c.run(3*time.Second, "session", "list", "--json")
	if err != nil {
		return []Session{}, err
	}
	var env struct {
		Sessions []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
			Default bool   `json:"default"`
			Dir     string `json:"session_dir"`
		} `json:"sessions"`
	}
	if json.Unmarshal(out, &env) != nil {
		return []Session{}, nil
	}
	ss := make([]Session, 0, len(env.Sessions))
	for _, s := range env.Sessions {
		ss = append(ss, Session{Name: s.Name, Running: s.Running, Default: s.Default, Dir: s.Dir})
	}
	return ss, nil
}

// parse traduce la respuesta de `herdr api snapshot` a nuestro State.
func (c Client) parse(out []byte) State {
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
	c.enrichPrograms(st.Panes)
	return st
}

// ── Detección del programa que corre (Claude Code / Kimi / …) ────────────
var (
	progMu    sync.Mutex
	progCache = map[string]progEntry{}
)

// Caché de snapshots REMOTOS para "stale-serve": si un VPS deja de responder un
// instante (SSH lento, blip de red), seguimos mostrando su último estado bueno
// (marcado Stale) en vez de parpadear a "desconectado". Sólo remoto — el local
// es barato y siempre fresco.
var (
	snapMu    sync.Mutex
	snapCache = map[string]State{}
	snapAt    = map[string]time.Time{}
)

func snapRemember(target string, st State) {
	snapMu.Lock()
	snapCache[target] = st
	snapAt[target] = time.Now()
	snapMu.Unlock()
}

// snapRecent devuelve el último snapshot bueno del target si es < maxAge.
func snapRecent(target string, maxAge time.Duration) (State, bool) {
	snapMu.Lock()
	defer snapMu.Unlock()
	st, ok := snapCache[target]
	if !ok || time.Since(snapAt[target]) > maxAge {
		return State{}, false
	}
	return st, true
}

type progEntry struct {
	prog string
	at   time.Time
}

// enrichPrograms llena Pane.Program vía process-info, SOLO para panes con un
// agente detectado (status != unknown). Cache de 15 s por pane, en paralelo:
// no infla el snapshot.
func (c Client) enrichPrograms(panes []Pane) {
	// en remoto cada detección es un SSH; el programa casi no cambia → caché
	// larga (60s) para no inundar de round-trips. Local barato → 15s.
	ttl := 15 * time.Second
	if c.target != "" {
		ttl = 60 * time.Second
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // cap de detecciones concurrentes (procesos ssh)
	for i := range panes {
		if panes[i].AgentStatus == "" || panes[i].AgentStatus == "unknown" {
			continue
		}
		id := panes[i].PaneID
		ckey := c.target + "\x00" + id // por-máquina: los pane_id colisionan entre hosts
		progMu.Lock()
		e, ok := progCache[ckey]
		progMu.Unlock()
		if ok && time.Since(e.at) < ttl {
			panes[i].Program = e.prog
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			prog := c.detectProgram(id)
			progMu.Lock()
			progCache[ckey] = progEntry{prog, time.Now()}
			progMu.Unlock()
			panes[idx].Program = prog
		}(i)
	}
	wg.Wait()
	pruneProgCache()
}

// pruneProgCache evita el crecimiento sin fin del caché: si se hace grande,
// suelta las entradas más viejas que 2 min. Barato y sólo cuando hace falta.
func pruneProgCache() {
	progMu.Lock()
	defer progMu.Unlock()
	if len(progCache) < 256 {
		return
	}
	for k, e := range progCache {
		if time.Since(e.at) > 2*time.Minute {
			delete(progCache, k)
		}
	}
}

var agentPrograms = []struct{ needle, label string }{
	{"claude", "Claude Code"}, {"kimi", "Kimi"}, {"codex", "Codex"},
	{"opencode", "OpenCode"}, {"cursor", "Cursor"}, {"aider", "Aider"},
	{"gemini", "Gemini"}, {"vertex", "Vertex"}, {"amp", "Amp"}, {"copilot", "Copilot"},
}

func (c Client) detectProgram(paneID string) string {
	out, err := c.run(3*time.Second, "pane", "process-info", "--pane", paneID)
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
func SessionStop(name string) error { return Client{}.SessionStop(name) }
func (c Client) SessionStop(name string) error {
	if name == "" {
		name = "default"
	}
	_, err := c.run(6*time.Second, "session", "stop", name)
	return err
}

// SessionDelete borra una sesión de herdr (el registro que "nunca se borra").
// herdr sólo deja borrar sesiones paradas; si está corriendo, herdr devuelve
// error y lo propagamos.
func SessionDelete(name string) error { return Client{}.SessionDelete(name) }
func (c Client) SessionDelete(name string) error {
	if name == "" {
		return fmt.Errorf("falta el nombre de la sesión")
	}
	_, err := c.run(6*time.Second, "session", "delete", name)
	return err
}

// RemoteHarnessdSnapshot corre `harnessd snapshot` en un VPS por SSH y devuelve
// el JSON crudo del snapshot completo (tareas, sesiones, costo…). Es cómo el
// panel local trae los DATOS de una máquina remota, no sólo sus terminales.
// wsPath = ruta del workspace del harness EN el VPS (quoteada, anti-inyección).
func RemoteHarnessdSnapshot(target, wsPath string) ([]byte, error) {
	if target == "" {
		return nil, fmt.Errorf("destino vacío")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := remotePATH + "harnessd snapshot"
	if wsPath != "" {
		cmd += " --workspace " + shQuote(wsPath)
	}
	out, err := exec.CommandContext(ctx, "ssh", append(sshBase(), target, cmd)...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
	}
	return out, err
}

// ServerStart arranca el server de herdr HEADLESS (sin TUI) y DESACOPLADO, tal
// como el usuario quiere "activarlo por debajo". Local: Setsid (sobrevive al
// daemon). Remoto: nohup en el VPS para que la sesión SSH regrese sin bloquear.
func ServerStart() error { return Client{}.ServerStart() }
func (c Client) ServerStart() error {
	if c.target != "" {
		// remoto: arrancar detached en el VPS; ssh regresa de inmediato.
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		remote := remotePATH + "nohup herdr server </dev/null >/dev/null 2>&1 &"
		args := append(sshBase(), c.target, remote)
		return exec.CommandContext(ctx, "ssh", args...).Run()
	}
	if _, err := exec.LookPath("herdr"); err != nil {
		return fmt.Errorf("herdr no está instalado en esta máquina")
	}
	cmd := exec.Command("herdr", "server")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // sin zombies
	return nil
}

// PaneRead devuelve el terminal EN VIVO de un pane (redactado — una terminal
// puede tener un token en pantalla). --source visible = lo que se ve ahora.
// format "ansi" conserva los colores SGR (el frontend los renderiza).
func PaneRead(paneID string, lines int, format string) (string, error) {
	return Client{}.PaneRead(paneID, lines, format)
}
func (c Client) PaneRead(paneID string, lines int, format string) (string, error) {
	if paneID == "" {
		return "", nil
	}
	if lines <= 0 || lines > 200 {
		lines = 60
	}
	if format != "ansi" {
		format = "text"
	}
	out, err := c.run(4*time.Second, "pane", "read", paneID,
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
func PaneSend(paneID, text string) error { return Client{}.PaneSend(paneID, text) }
func (c Client) PaneSend(paneID, text string) error {
	_, err := c.run(5*time.Second, "pane", "run", paneID, text)
	return err
}

// ── Control de ciclo de vida (destructivo — el caller valida el id) ───────

// Interrupt manda Ctrl-C a un pane (send-keys C-c) — corta el proceso sin
// cerrar la terminal.
func Interrupt(paneID string) error { return Client{}.Interrupt(paneID) }
func (c Client) Interrupt(paneID string) error {
	_, err := c.run(5*time.Second, "pane", "send-keys", paneID, "C-c")
	return err
}

// ClosePane cierra un pane (mata su terminal).
func ClosePane(paneID string) error { return Client{}.ClosePane(paneID) }
func (c Client) ClosePane(paneID string) error {
	_, err := c.run(5*time.Second, "pane", "close", paneID)
	return err
}

// CloseTab cierra un tab (y sus panes).
func CloseTab(tabID string) error { return Client{}.CloseTab(tabID) }
func (c Client) CloseTab(tabID string) error {
	_, err := c.run(5*time.Second, "tab", "close", tabID)
	return err
}

// CloseWorkspace cierra un workspace entero.
func CloseWorkspace(wsID string) error { return Client{}.CloseWorkspace(wsID) }
func (c Client) CloseWorkspace(wsID string) error {
	_, err := c.run(5*time.Second, "workspace", "close", wsID)
	return err
}

// ── Teclas (respuestas interactivas de menú) ─────────────────────────────

// PaneKeys manda teclas literales a un pane (herdr pane send-keys). Para
// contestar un menú de agente: la tecla del número, o "Enter"/"y"/"n". El
// caller valida el pane contra el snapshot.
func PaneKeys(paneID string, keys []string) error { return Client{}.PaneKeys(paneID, keys) }
func (c Client) PaneKeys(paneID string, keys []string) error {
	if paneID == "" || len(keys) == 0 {
		return nil
	}
	args := append([]string{"pane", "send-keys", paneID}, keys...)
	_, err := c.run(5*time.Second, args...)
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
	return Client{}.WorkspaceCreate(label, cwd)
}
func (c Client) WorkspaceCreate(label, cwd string) (string, error) {
	args := []string{"workspace", "create", "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	out, err := c.run(6*time.Second, args...)
	if err != nil {
		return "", err
	}
	return idFrom(out, "result", "workspace", "workspace_id"), nil
}

// TabCreate abre una terminal nueva (tab) en un workspace.
func TabCreate(wsID, label, cwd string) (string, error) {
	return Client{}.TabCreate(wsID, label, cwd)
}
func (c Client) TabCreate(wsID, label, cwd string) (string, error) {
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
	out, err := c.run(6*time.Second, args...)
	if err != nil {
		return "", err
	}
	return idFrom(out, "result", "tab", "tab_id"), nil
}

// PaneSplit divide un pane (terminal lado a lado). dir: right | down.
func PaneSplit(paneID, dir, cwd string) (string, error) {
	return Client{}.PaneSplit(paneID, dir, cwd)
}
func (c Client) PaneSplit(paneID, dir, cwd string) (string, error) {
	if dir != "right" && dir != "down" {
		dir = "down"
	}
	args := []string{"pane", "split", paneID, "--direction", dir, "--no-focus"}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	out, err := c.run(6*time.Second, args...)
	if err != nil {
		return "", err
	}
	return idFrom(out, "result", "pane", "pane_id"), nil
}
