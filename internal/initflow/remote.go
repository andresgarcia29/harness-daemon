package initflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/api"
	"github.com/andresgarcia29/harness-daemon/internal/gen"
	"github.com/andresgarcia29/harness-daemon/internal/herdr"
)

// ── instalación remota (F11, ADR-0011 §4) ──
// El wizard corre LOCAL; el harness nace en un VPS. Como cada paso es un
// subcomando CLI, lo remoto es el MISMO código por ssh:
//   ssh <target> harness <init-step|discover|generate> --json
// Contrato de streams: stderr = progreso humano (→ LogBuffer), stdout = JSON.
// Los secretos viajan por STDIN de ssh, jamás en argv.

var errNoResolver = errors.New("targets no disponibles en este daemon")

func (m *Manager) isRemote() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st.Target != ""
}

func (m *Manager) sshTarget() (string, error) {
	m.mu.Lock()
	name := m.st.Target
	m.mu.Unlock()
	if m.resolveTarget == nil {
		return "", errNoResolver
	}
	ssh, ok := m.resolveTarget(name)
	if !ok {
		return "", fmt.Errorf("target desconocido: %s", name)
	}
	return ssh, nil
}

// remoteExec corre argv arbitrario en el target, streaming stderr al log del paso.
func (m *Manager) remoteExec(step string, argv []string, stdin []byte, timeout time.Duration) ([]byte, error) {
	ssh, err := m.sshTarget()
	if err != nil {
		return nil, err
	}
	return herdr.Exec(ssh, argv, stdin, timeout, func(l string) {
		if sshNoise(l) {
			return
		}
		m.logs.Append(step, l)
	})
}

// remoteExecQuiet — igual pero SIN loguear (validaciones dry_run: el tecleo
// del usuario no debe ensuciar la bitácora del paso).
func (m *Manager) remoteExecQuiet(argv []string, stdin []byte, timeout time.Duration) ([]byte, error) {
	ssh, err := m.sshTarget()
	if err != nil {
		return nil, err
	}
	return herdr.Exec(ssh, argv, stdin, timeout, nil)
}

// sshNoise — avisos de plomería de ssh que no le dicen nada al usuario.
func sshNoise(l string) bool {
	return strings.Contains(l, "ControlSocket") ||
		strings.Contains(l, "disabling multiplexing") ||
		strings.HasPrefix(l, "Warning: Permanently added")
}

// remoteHarness corre `harness <args>` en el target.
func (m *Manager) remoteHarness(step string, args []string, stdin []byte, timeout time.Duration) ([]byte, error) {
	return m.remoteExec(step, append([]string{"harness"}, args...), stdin, timeout)
}

// ensureRemoteBinaryOnce — el sync UNA vez por vida del daemon (cache): las
// ACCIONES remotas (sonda MCP, certificar secretos) también lo necesitan, no
// solo los pasos — llamar al VPS con un binario viejo da 'exit status 2' con
// el usage de flags que aún no existen (bug real del paso MCPs).
func (m *Manager) ensureRemoteBinaryOnce(step string) error {
	m.mu.Lock()
	ok := m.remoteBinOK
	m.mu.Unlock()
	if ok {
		return nil
	}
	if err := m.ensureRemoteBinary(step); err != nil {
		return err
	}
	m.mu.Lock()
	m.remoteBinOK = true
	m.mu.Unlock()
	return nil
}

// ensureRemoteBinary verifica `harness` en el VPS y lo SINCRONIZA a la
// versión local: local y remoto hablan un protocolo (progreso @@repo, shapes
// JSON) — un binario viejo allá rompe features nuevas acá en silencio (fue
// el bug de los checks de clone que no se movían). Si no puede instalar, el
// error trae la instrucción exacta.
func (m *Manager) ensureRemoteBinary(step string) error {
	if out, err := m.remoteHarness(step, []string{"version"}, nil, 15*time.Second); err == nil {
		v := strings.TrimSpace(string(out))
		// dev = builds locales (tests, dev loop): no hay release que pinear
		if v == m.version || m.version == "dev" || v == "dev" {
			m.logs.Append(step, "harness en el VPS: v"+v+" ✓")
			return nil
		}
		m.logs.Append(step, "harness en el VPS v"+v+" ≠ local v"+m.version+" — lo sincronizo…")
	} else {
		m.logs.Append(step, "harness no está en el VPS — lo instalo del release público…")
	}
	uname, err := m.remoteExec(step, []string{"uname", "-sm"}, nil, 15*time.Second)
	if err != nil {
		return fmt.Errorf("no pude hablar con el VPS: %w", err)
	}
	fields := strings.Fields(strings.ToLower(string(uname)))
	if len(fields) != 2 {
		return fmt.Errorf("uname raro en el VPS: %q", uname)
	}
	osName := fields[0]
	arch := fields[1]
	switch arch {
	case "x86_64", "amd64":
		arch = "amd64"
	case "aarch64", "arm64":
		arch = "arm64"
	default:
		return fmt.Errorf("arquitectura sin binario: %s", arch)
	}
	rel := "latest/download"
	if m.version != "dev" {
		rel = "download/v" + m.version // la MISMA versión que el orquestador local
	}
	url := fmt.Sprintf("https://github.com/andresgarcia29/harness-daemon/releases/%s/harnessd-%s-%s", rel, osName, arch)
	script := `mkdir -p "$HOME/.local/bin" && curl -fsSL -o "$HOME/.local/bin/harness" ` + url +
		` && chmod +x "$HOME/.local/bin/harness" && "$HOME/.local/bin/harness" version`
	if out, err := m.remoteExec(step, []string{"sh", "-c", script}, nil, 2*time.Minute); err != nil {
		return fmt.Errorf("no pude instalar harness en el VPS (%v) — instálalo tú: brew install andresgarcia29/agm/harness, o baja %s a ~/.local/bin/harness", err, url)
	} else {
		m.logs.Append(step, "✓ harness instalado en el VPS: v"+strings.TrimSpace(string(out)))
	}
	return nil
}

// ── workspace remoto ──

// shq — quoting POSIX para rutas dentro de scripts sh remotos.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// remoteProbeDir — el dry_run remoto con sh PURO: validar una carpeta no
// necesita el binario harness en el VPS (ese solo hace falta al continuar, y
// ahí se auto-instala). Sin esto, cada tecleo del usuario disparaba un
// `harness: command not found` rojo — el bug del primer intento real.
func (m *Manager) remoteProbeDir(path string, confirmOutside bool) (any, int) {
	// validación sintáctica local (las mismas reglas que normalizeWS,
	// menos el home: ese es el del VPS y se resuelve allá)
	if strings.Contains(path, "..") {
		return map[string]any{"ok": false, "error": "la ruta no puede contener «..»"}, 400
	}
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "/") {
		return map[string]any{"ok": false, "error": "la ruta debe ser absoluta (o empezar con ~/)"}, 400
	}
	script := `p=` + shq(path) + `
case "$p" in "~") p="$HOME";; "~/"*) p="$HOME/${p#\~/}";; esac
case "$p" in "$HOME"|"$HOME"/*) in_home=1;; *) in_home=0;; esac
norm=$p
if [ -d "$p" ]; then
  st=exists
  [ -w "$p" ] && wr=1 || wr=0
  [ -z "$(ls -A "$p" 2>/dev/null)" ] && emp=1 || emp=0
  [ -f "$p/.harness-version" ] && inst=1 || inst=0
else
  st=absent; emp=1; inst=0
  d=$(dirname "$p"); while [ ! -d "$d" ] && [ "$d" != "/" ]; do d=$(dirname "$d"); done
  [ -w "$d" ] && wr=1 || wr=0
fi
echo "PROBE|$norm|$st|$wr|$emp|$inst|$in_home"`
	out, err := m.remoteExecQuiet([]string{"sh", "-c", script}, nil, 15*time.Second)
	if err != nil {
		return map[string]any{"ok": false, "error": "no pude hablar con el VPS: " + err.Error()}, 502
	}
	var norm string
	var exists, writable, empty, installed, inHome bool
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "PROBE|") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 7 {
			continue
		}
		norm = f[1]
		exists, writable, empty = f[2] == "exists", f[3] == "1", f[4] == "1"
		installed, inHome = f[5] == "1", f[6] == "1"
	}
	if norm == "" {
		return map[string]any{"ok": false, "error": "respuesta rara del VPS"}, 502
	}
	if !inHome && !confirmOutside {
		return map[string]any{"ok": false, "error": "fuera del home del VPS — si es a propósito, confirma con confirm_outside_home"}, 400
	}
	if installed {
		return map[string]any{"ok": false, "error": "ese workspace del VPS YA tiene un harness instalado"}, 409
	}
	return map[string]any{"ok": true, "normalized": norm,
		"exists": exists, "writable": writable, "empty": empty}, 200
}

func (m *Manager) handleWorkspaceRemote(body map[string]any) (any, int) {
	path := str(body, "path")
	if path == "" {
		return map[string]any{"ok": false, "error": "ruta vacía"}, 400
	}
	dry := boolv(body, "dry_run")
	if dry {
		return m.remoteProbeDir(path, boolv(body, "confirm_outside_home"))
	}
	if err := m.ensureRemoteBinaryOnce("workspace"); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 502
	}
	args := []string{"init-step", "workspace", "--path", path, "--json"}
	if boolv(body, "create") {
		args = append(args, "--create")
	}
	if boolv(body, "confirm_outside_home") {
		args = append(args, "--confirm-outside-home")
	}
	out, err := m.remoteHarness("workspace", args, nil, 30*time.Second)
	if err != nil && len(out) == 0 {
		return map[string]any{"ok": false, "error": "VPS: " + err.Error()}, 502
	}
	var res map[string]any
	if json.Unmarshal(out, &res) != nil {
		return map[string]any{"ok": false, "error": "respuesta rara del VPS: " + strings.TrimSpace(string(out))}, 502
	}
	if res["ok"] != true {
		return res, 400
	}
	norm, _ := res["workspace"].(string)
	m.mu.Lock()
	if m.st.Workspace != "" && m.st.Workspace != norm {
		cur := m.st.Workspace
		m.mu.Unlock()
		return map[string]any{"ok": false, "error": "este init ya está anclado a " + cur}, 409
	}
	m.st.Workspace = norm
	m.logs.Append("workspace", "workspace remoto fijado en "+m.st.Target+":"+norm)
	m.setStepLocked("workspace", OK, m.st.Target+":"+norm, "")
	m.mu.Unlock()
	return res, 200
}

// ── github remoto: validar local, token al VPS por stdin ──

func (m *Manager) handleGithubRemote(body map[string]any) (any, int) {
	switch str(body, "mode") {
	case "gh":
		// el token vive en el gh del VPS; lo validamos contra la API desde aquí
		out, err := m.remoteExec("github", []string{"gh", "auth", "token"}, nil, 20*time.Second)
		if err != nil {
			return map[string]any{"ok": false, "error": "gh en el VPS: " + err.Error()}, 400
		}
		tok := strings.TrimSpace(string(out))
		user, _, err := validatePATFn(tok)
		if err != nil {
			return map[string]any{"ok": false, "error": "el token del gh remoto no valida: " + err.Error()}, 400
		}
		m.mu.Lock()
		m.st.GitHub = &GHState{Mode: "gh", User: user}
		m.setStepLocked("github", OK, "gh (VPS) · "+user, "")
		m.mu.Unlock()
		return map[string]any{"ok": true, "mode": "gh", "user": user}, 200
	case "pat":
		tok := str(body, "token")
		if tok == "" {
			return map[string]any{"ok": false, "error": "token vacío"}, 400
		}
		user, scopes, err := validatePATFn(tok)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, 400
		}
		// al VPS por STDIN (umask 077), jamás en argv
		script := `umask 077 && mkdir -p "$HOME/.config/harness" && cat > "$HOME/.config/harness/github-token"`
		if _, err := m.remoteExec("github", []string{"sh", "-c", script}, []byte(tok+"\n"), 30*time.Second); err != nil {
			return map[string]any{"ok": false, "error": "no pude guardar el token en el VPS: " + err.Error()}, 502
		}
		m.mu.Lock()
		m.st.GitHub = &GHState{Mode: "pat", User: user}
		m.logs.Append("github", "PAT validado y guardado en el VPS (0600) — usuario "+user)
		m.setStepLocked("github", OK, "PAT (VPS) · "+user, "")
		m.mu.Unlock()
		return map[string]any{"ok": true, "mode": "pat", "user": user, "scopes": scopes}, 200
	default:
		return map[string]any{"ok": false, "error": "mode debe ser gh | pat"}, 400
	}
}

// ── pasos remotos ──

func (m *Manager) remoteClone(ws, mode string, sel []RepoSel) error {
	if len(sel) == 0 {
		return errors.New("no hay repos seleccionados")
	}
	parts := make([]string, 0, len(sel))
	idxOf := map[string]int{}
	for i, r := range sel {
		p := r.FullName
		if r.Ref != "" {
			p += "@" + r.Ref
		}
		parts = append(parts, p)
		idxOf[r.FullName] = i
	}
	sshDest, err := m.sshTarget()
	if err != nil {
		return err
	}
	argv := []string{"harness", "init-step", "clone", "--workspace", ws, "--source", mode, "--repos", strings.Join(parts, ","), "--json"}
	// stderr trae DOS canales: líneas @@repo|… (progreso estructurado → los
	// checks por repo se prenden EN VIVO) y el resto (progreso humano → log).
	out, err := herdr.Exec(sshDest, argv, nil, 30*time.Minute, func(l string) {
		if rest, ok := strings.CutPrefix(l, "@@repo|"); ok {
			f := strings.SplitN(rest, "|", 3)
			if len(f) == 3 {
				if i, ok := idxOf[f[0]]; ok {
					m.setRepoStatus(i, Status(f[1]), f[2])
				}
			}
			return
		}
		if sshNoise(l) {
			return
		}
		m.logs.Append("clone", l)
	})
	var res struct {
		Repos []RepoSel `json:"repos"`
		Fails int       `json:"fails"`
	}
	if json.Unmarshal(out, &res) == nil && len(res.Repos) == len(sel) {
		for i, r := range res.Repos {
			m.setRepoStatus(i, r.Status, r.Error)
		}
	}
	if err != nil {
		return fmt.Errorf("clone en el VPS: %w", err)
	}
	if res.Fails > 0 {
		return fmt.Errorf("%d repo(s) fallaron en el VPS — reintenta", res.Fails)
	}
	return nil
}

func (m *Manager) remoteRequirements() error {
	out, err := m.remoteHarness("requirements", []string{"init-step", "requirements", "--json"}, nil, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("requirements en el VPS: %w", err)
	}
	var res struct {
		Requirements []ReqState `json:"requirements"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return fmt.Errorf("respuesta rara del VPS: %v", err)
	}
	var missing []string
	for i := range res.Requirements {
		res.Requirements[i].AutoRun = false // instalar en remoto = a mano (sudo)
		if !res.Requirements[i].OK && !res.Requirements[i].Optional {
			missing = append(missing, res.Requirements[i].Name)
		}
		m.logs.Append("requirements", reqLine(res.Requirements[i]))
	}
	m.mu.Lock()
	m.st.Requirements = res.Requirements
	m.persistLocked()
	m.mu.Unlock()
	if len(missing) > 0 {
		return fmt.Errorf("faltan en el VPS: %s — instálalos allá y reintenta", strings.Join(missing, ", "))
	}
	return nil
}

func (m *Manager) remoteDiscover(ws string) error {
	out, err := m.remoteHarness("discover", []string{"discover", "--workspace", ws, "--json"}, nil, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("discover en el VPS: %w", err)
	}
	var inv gen.Inventory
	if err := json.Unmarshal(out, &inv); err != nil {
		return fmt.Errorf("inventory del VPS no parsea: %v", err)
	}
	m.mu.Lock()
	m.inv = &inv
	m.mu.Unlock()
	m.logs.Append("discover", fmt.Sprintf("✓ %d repos inventariados en el VPS · hints: %v", inv.RepoCount, inv.SecretHints))
	m.seedAnswersIfEmpty()
	m.applyCoverage("discover")
	return nil
}

func (m *Manager) remoteGenerate(ws string, a *gen.Answers) error {
	answers, err := json.Marshal(a)
	if err != nil {
		return err
	}
	m.logs.Append("generate", "generando en el VPS (answers por stdin)…")
	out, err := m.remoteHarness("generate",
		[]string{"generate", "--workspace", ws, "--answers", "-", "--json"}, answers, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("generate en el VPS: %w", err)
	}
	var rep gen.Report
	if err := json.Unmarshal(out, &rep); err != nil {
		return fmt.Errorf("reporte del VPS no parsea: %v", err)
	}
	m.logs.Append("generate", fmt.Sprintf("✓ VPS: %d creados · %d actualizados · %d intactos · %d conflictos",
		rep.Created, rep.Updated, rep.Kept, rep.Conflicts))
	if rep.Conflicts > 0 {
		m.logs.Append("generate", "revisa los .new en el VPS — lo personalizado se conservó")
	}
	return nil
}

func (m *Manager) remoteArchaeology(ws string) error {
	out, err := m.remoteHarness("archaeology",
		[]string{"init-step", "archaeology", "--workspace", ws, "--json"}, nil, 30*time.Minute)
	var res struct {
		Results []ArchState `json:"results"`
		Fails   int         `json:"fails"`
	}
	if json.Unmarshal(out, &res) == nil {
		m.mu.Lock()
		m.st.Archaeology = res.Results
		m.persistLocked()
		m.mu.Unlock()
	}
	if err != nil {
		return fmt.Errorf("arqueología en el VPS: %w", err)
	}
	if res.Fails > 0 {
		return fmt.Errorf("%d cluster(s) sin arqueología en el VPS — reintenta o salta", res.Fails)
	}
	return nil
}

func (m *Manager) remoteFirstTask(ws string) (int, error) {
	out, err := m.remoteHarness("first-task",
		[]string{"init-step", "first-task", "--workspace", ws, "--json"}, nil, 30*time.Second)
	if err != nil {
		return 0, fmt.Errorf("VPS: %w", err)
	}
	var res struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(out, &res)
	return res.Count, nil
}

func (m *Manager) remoteFinish(ws string) error {
	_, err := m.remoteHarness("finish",
		[]string{"init-step", "finish", "--workspace", ws}, nil, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("el doctor en el VPS reporta fallos: %w", err)
	}
	// el target del panel apunta ahora al workspace recién nacido: el
	// selector de máquina trae sus tareas/sesiones/costo sin configurar nada
	m.mu.Lock()
	name := m.st.Target
	m.mu.Unlock()
	if sshDest, err := m.sshTarget(); err == nil {
		if err := api.AddTarget(name, sshDest, ws); err == nil {
			m.logs.Append("finish", "✓ target «"+name+"» apunta al workspace nuevo — el selector de máquina ya lo observa")
		}
	}
	return nil
}
