// op.go — el plano de OPERAR del daemon (ADR-0010: crear trabajo, jamás
// merges). Port del panel de Python, con los mismos guardrails:
//
//   - Token anti-CSRF por arranque, inyectado en el HTML servido y exigido en
//     el header X-Corvux-Token de todo POST (un <form> ajeno no puede poner
//     headers custom sin un preflight CORS que este server jamás contesta).
//   - Check de Host contra DNS rebinding. Solo 127.0.0.1 (el listener ya).
//   - Secretos write-only: se validan contra el proveedor ANTES de guardarse
//     (0600), jamás se devuelven, jamás se loguean, jamás pasan por un agente.
//   - Todo lo lanzado pasa por LOS MISMOS gates: a main solo se llega por
//     ship.sh. Aquí no hay botón de merge.
package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/herdr"
	"github.com/andresgarcia29/harness-daemon/internal/ident"
	"github.com/andresgarcia29/harness-daemon/internal/redact"
)

// Overridables para tests (httptest) — en producción son los reales.
var (
	LinearURL     = "https://api.linear.app/graphql"
	OpenRouterKey = "https://openrouter.ai/api/v1/key"
	OpenRouterMod = "https://openrouter.ai/api/v1/models"
)

// Op es el plano de operar de UN workspace.
type Op struct {
	Token string // por arranque; viaja en el HTML, vuelve en el header
	WS    string // ruta del workspace
	WSID  string
	DB    *sql.DB
	Port  int
}

func NewOp(ws, wsID string, db *sql.DB, port int) *Op {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &Op{Token: hex.EncodeToString(b), WS: ws, WSID: wsID, DB: db, Port: port}
}

// Guard valida token + Host + tamaño. Devuelve el body o escribe el error.
func (o *Op) Guard(rw http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	host := r.Host
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	if host != "127.0.0.1" && host != "localhost" {
		http.Error(rw, `{"ok":false,"error":"host no permitido"}`, 403)
		return nil, false
	}
	if r.Header.Get("X-Corvux-Token") != o.Token {
		http.Error(rw, `{"ok":false,"error":"token de operacion invalido - recarga la pagina"}`, 403)
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
	if err != nil {
		http.Error(rw, `{"ok":false,"error":"body ilegible"}`, 400)
		return nil, false
	}
	var b map[string]any
	if len(body) > 0 {
		if json.Unmarshal(body, &b) != nil {
			http.Error(rw, `{"ok":false,"error":"json invalido"}`, 400)
			return nil, false
		}
	}
	if b == nil {
		b = map[string]any{}
	}
	return b, true
}

func s(b map[string]any, k string) string {
	v, _ := b[k].(string)
	return strings.TrimSpace(v)
}

func writeJSON(rw http.ResponseWriter, code int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(v)
}

func fail(rw http.ResponseWriter, code int, msg string) {
	writeJSON(rw, code, map[string]any{"ok": false, "error": msg})
}

// ── lanzar claude headless (desacoplado; HARNESS_CLAUDE_BIN para tests) ──
func (o *Op) launch(args []string, logname string) (int, error) {
	bin := os.Getenv("HARNESS_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return 0, fmt.Errorf("no encuentro el CLI '%s' en PATH — el plano de operar lanza agentes reales y lo necesita", bin)
	}
	logdir := filepath.Join(o.WS, ".harness", "runs")
	_ = os.MkdirAll(logdir, 0o755)
	lf, err := os.OpenFile(filepath.Join(logdir, logname), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = o.WS
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		lf.Close()
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait(); lf.Close() }() // no zombies
	return pid, nil
}

func (o *Op) emit(kind, summary, task string) {
	_ = os.MkdirAll(filepath.Join(o.WS, ".harness"), 0o755)
	f, err := os.OpenFile(filepath.Join(o.WS, ".harness", "events.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	e := map[string]any{"ts": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"kind": kind, "task": task, "actor": "panel", "summary": redact.Clip(summary, 400)}
	b, _ := json.Marshal(e)
	_, _ = f.Write(append(b, '\n'))
}

func (o *Op) recordRun(task, session string, pid int, kind string) {
	_ = os.MkdirAll(filepath.Join(o.WS, ".harness"), 0o755)
	f, err := os.OpenFile(filepath.Join(o.WS, ".harness", "runs.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"ts": time.Now().Unix(), "task": task,
		"session": session, "pid": pid, "kind": kind})
	_, _ = f.Write(append(b, '\n'))
}

var slugRe2 = regexp.MustCompile(`[^a-z0-9]+`)
var uuidHex = "0123456789abcdef"

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// OpTask crea tasks/<id>/task.md (el frontmatter que /auto respeta como ley)
// y lanza claude -p "/auto <id>" con --session-id CONOCIDO.
func (o *Op) OpTask(rw http.ResponseWriter, r *http.Request) {
	b, ok := o.Guard(rw, r)
	if !ok {
		return
	}
	title, origin, ticket := s(b, "title"), s(b, "origin"), s(b, "ticket")
	if origin == "" {
		origin = "prompt"
	}
	if origin == "ticket" && ticket == "" {
		fail(rw, 400, "origen ticket sin id de ticket")
		return
	}
	if origin != "ticket" && title == "" {
		fail(rw, 400, "falta el título")
		return
	}
	tid := ticket
	if origin != "ticket" {
		slug := strings.Trim(slugRe2.ReplaceAllString(strings.ToLower(title), "-"), "-")
		parts := strings.SplitN(slug, "-", 4)
		if len(parts) > 3 {
			parts = parts[:3]
		}
		slug = strings.Join(parts, "-")
		if slug == "" {
			slug = "tarea"
		}
		tid = "AUTO-" + time.Now().UTC().Format("20060102") + "-" + slug
		for n := 2; ; n++ {
			if _, err := os.Stat(filepath.Join(o.WS, "tasks", tid)); os.IsNotExist(err) {
				break
			}
			tid = fmt.Sprintf("AUTO-%s-%s-%d", time.Now().UTC().Format("20060102"), slug, n)
		}
	}
	maxPar := 3
	if v, okf := b["max_parallel"].(float64); okf {
		maxPar = int(v)
		if maxPar < 1 {
			maxPar = 1
		}
		if maxPar > 12 {
			maxPar = 12
		}
	}
	asum := true
	if v, okb := b["assumptions_ok"].(bool); okb {
		asum = v
	}
	review, _ := b["review_before_ship"].(bool)
	prio := s(b, "priority")
	if prio == "" {
		prio = "P2"
	}
	tdir := filepath.Join(o.WS, "tasks", tid)
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		fail(rw, 500, "no pude crear la tarea: "+err.Error())
		return
	}
	fm := []string{"---", "id: " + tid,
		`title: "` + strings.ReplaceAll(title, `"`, `'`) + `"`,
		"origin: " + map[bool]string{true: "ticket", false: "prompt"}[origin == "ticket"],
		"source: panel", "priority: " + prio,
		fmt.Sprintf("max_parallel: %d", maxPar),
		fmt.Sprintf("assumptions_ok: %t", asum),
		fmt.Sprintf("review_before_ship: %t", review)}
	if m := s(b, "model"); m != "" {
		fm = append(fm, "preferred_model: "+m)
	}
	if bg := s(b, "budget"); bg != "" {
		fm = append(fm, "budget_usd: "+bg)
	}
	fm = append(fm, "created: "+time.Now().UTC().Format("2006-01-02T15:04:05Z"), "---", "")
	body := s(b, "context")
	content := strings.Join(fm, "\n")
	if body != "" {
		content += body + "\n"
	}
	if err := os.WriteFile(filepath.Join(tdir, "task.md"), []byte(content), 0o644); err != nil {
		fail(rw, 500, err.Error())
		return
	}
	sid := newUUID()
	target := tid
	if origin == "ticket" {
		target = ticket
	}
	args := []string{"-p", "/auto " + target, "--session-id", sid}
	if m := s(b, "model"); m != "" {
		args = append(args, "--model", m)
	}
	pid, err := o.launch(args, tid+".log")
	if err != nil {
		fail(rw, 400, err.Error())
		return
	}
	o.recordRun(tid, sid, pid, "auto")
	o.emit("phase", "intake — tarea creada desde el panel y lanzada (sesión "+sid[:8]+"…)", tid)
	writeJSON(rw, 200, map[string]any{"ok": true, "id": tid, "session": sid, "pid": pid})
}

// OpRespond reanuda una sesión de Claude Code con texto (claude --resume).
func (o *Op) OpRespond(rw http.ResponseWriter, r *http.Request) {
	b, ok := o.Guard(rw, r)
	if !ok {
		return
	}
	session, text := s(b, "session"), s(b, "text")
	if session == "" || text == "" {
		fail(rw, 400, "faltan session o text")
		return
	}
	short := session
	if len(short) > 8 {
		short = short[:8]
	}
	pid, err := o.launch([]string{"-p", text, "--resume", session}, "respond-"+short+".log")
	if err != nil {
		fail(rw, 400, err.Error())
		return
	}
	o.recordRun("", session, pid, "respond")
	o.emit("decision", "el humano respondió desde el panel: "+redact.Clip(text, 160), "")
	writeJSON(rw, 200, map[string]any{"ok": true, "session": session, "pid": pid})
}

// OpPaneSend manda texto + Enter a un pane de herdr — responderle a CUALQUIER
// agente (Claude Code, OpenCode, Codex…) desde la UI. El pane_id se valida
// contra el snapshot vivo de herdr: jamás pasamos strings arbitrarios al CLI.
func (o *Op) OpPaneSend(rw http.ResponseWriter, r *http.Request) {
	b, ok := o.Guard(rw, r)
	if !ok {
		return
	}
	pane, text := s(b, "pane"), s(b, "text")
	if pane == "" || text == "" {
		fail(rw, 400, "faltan pane o text")
		return
	}
	if len(text) > 4000 {
		fail(rw, 400, "texto demasiado largo (máx 4000)")
		return
	}
	st := herdr.Snapshot()
	if !st.Available {
		fail(rw, 400, "herdr no está corriendo")
		return
	}
	found := false
	for _, p := range st.Panes {
		if p.PaneID == pane {
			found = true
			break
		}
	}
	if !found {
		fail(rw, 404, "ese pane no existe en herdr (¿se cerró?)")
		return
	}
	if err := herdr.PaneSend(pane, text); err != nil {
		fail(rw, 500, "herdr no aceptó el texto: "+redact.Clip(err.Error(), 120))
		return
	}
	o.emit("decision", "el humano respondió a un agente (herdr "+pane+"): "+redact.Clip(text, 140), "")
	writeJSON(rw, 200, map[string]any{"ok": true, "pane": pane})
}

// OpConnect valida un token contra el proveedor ANTES de guardarlo (0600).
func (o *Op) OpConnect(rw http.ResponseWriter, r *http.Request) {
	b, ok := o.Guard(rw, r)
	if !ok {
		return
	}
	prov, token := s(b, "provider"), s(b, "token")
	if prov != "linear" && prov != "openrouter" {
		fail(rw, 400, "proveedor desconocido")
		return
	}
	if token == "" {
		fail(rw, 400, "falta el token")
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	var req *http.Request
	if prov == "linear" {
		req, _ = http.NewRequest("POST", LinearURL, strings.NewReader(`{"query":"{ viewer { id } }"}`))
		req.Header.Set("Authorization", token)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest("GET", OpenRouterKey, nil)
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		fail(rw, 400, "no pude llegar a "+prov)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		fail(rw, 400, fmt.Sprintf("token inválido (%s devolvió %d)", prov, resp.StatusCode))
		return
	}
	dir := ident.ConfigDir()
	_ = os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, prov+"-token"), []byte(token+"\n"), 0o600); err != nil {
		fail(rw, 500, "no pude guardar")
		return
	}
	writeJSON(rw, 200, map[string]any{"ok": true, "provider": prov, "connected": true})
}

// Connections: presencia de tokens (bool), jamás el valor.
func Connections() map[string]bool {
	out := map[string]bool{}
	for _, p := range []string{"linear", "openrouter"} {
		_, err := os.Stat(filepath.Join(ident.ConfigDir(), p+"-token"))
		out[p] = err == nil
	}
	return out
}

// SyncPrices cotiza desde OpenRouter los modelos OBSERVADOS sin precio y los
// persiste en la tabla prices (source=openrouter). Corre al arranque (auto,
// fail-open) y desde el botón. Cross-máquina: cada daemon sincroniza igual.
func SyncPrices(db *sql.DB) (added, missing []string, err error) {
	added, missing = []string{}, []string{}
	rows, err := db.Query(`SELECT DISTINCT c.model FROM calls c
		LEFT JOIN prices p ON p.model = c.model AND p.valid_from = 0
		WHERE p.model IS NULL`)
	if err != nil {
		return
	}
	var targets []string
	for rows.Next() {
		var m string
		if rows.Scan(&m) == nil && m != "" {
			targets = append(targets, m)
		}
	}
	rows.Close()
	if len(targets) == 0 {
		return
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", OpenRouterMod, nil)
	if b, e := os.ReadFile(filepath.Join(ident.ConfigDir(), "openrouter-token")); e == nil {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(b)))
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var cat struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&cat) != nil {
		return added, missing, fmt.Errorf("catálogo ilegible")
	}
	norm := func(x string) string {
		var sb strings.Builder
		for _, c := range strings.ToLower(x) {
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
				sb.WriteRune(c)
			}
		}
		return sb.String()
	}
	parseF := func(v string) float64 {
		var f float64
		_, _ = fmt.Sscanf(v, "%g", &f)
		return f
	}
	for _, t := range targets {
		var hitIn, hitOut float64
		found := false
		nt := norm(t)
		for _, m := range cat.Data {
			nid := norm(m.ID)
			last := nid
			if i := strings.LastIndex(m.ID, "/"); i >= 0 {
				last = norm(m.ID[i+1:])
			}
			if strings.Contains(nid, nt) || strings.Contains(nt, last) {
				hitIn, hitOut = parseF(m.Pricing.Prompt), parseF(m.Pricing.Completion)
				found = hitIn > 0
				if found {
					break
				}
			}
		}
		if found {
			in6, out6 := hitIn*1e6, hitOut*1e6
			_, e := db.Exec(`INSERT INTO prices (model, speed, valid_from, provider, input, output,
				cache_read, cache_write_5m, cache_write_1h, source)
				VALUES (?,'standard',0,'openrouter',?,?,?,?,?,'openrouter')
				ON CONFLICT(model, speed, valid_from) DO NOTHING`,
				t, in6, out6, in6*0.1, in6*1.25, in6*2.0)
			if e == nil {
				added = append(added, t)
			}
		} else {
			missing = append(missing, t)
		}
	}
	return
}

// OpSyncPrices: el botón.
func (o *Op) OpSyncPrices(rw http.ResponseWriter, r *http.Request) {
	if _, ok := o.Guard(rw, r); !ok {
		return
	}
	added, missing, err := SyncPrices(o.DB)
	if err != nil {
		fail(rw, 500, "no pude sincronizar: "+err.Error())
		return
	}
	note := ""
	if len(added) == 0 && len(missing) == 0 {
		note = "todos los modelos observados ya tienen precio"
	}
	writeJSON(rw, 200, map[string]any{"ok": true, "added": added, "missing": missing, "note": note})
}

// DBInfo: el estado REAL del almacén para la tarjeta de Conexiones.
func DBInfo(db *sql.DB, path string) map[string]any {
	info := map[string]any{"engine": "sqlite", "path": path}
	if fi, err := os.Stat(path); err == nil {
		info["size_bytes"] = fi.Size()
	}
	var machines, calls, events int64
	_ = db.QueryRow(`SELECT COUNT(*) FROM machines`).Scan(&machines)
	_ = db.QueryRow(`SELECT COUNT(*) FROM calls`).Scan(&calls)
	_ = db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events)
	info["machines"], info["calls"], info["events"] = machines, calls, events
	return info
}

// OpHerdr — control de ciclo de vida de herdr (interrumpir, cerrar pane/tab/
// workspace). Toda acción es DESTRUCTIVA: el frontend pide confirmación. El id
// se valida contra el snapshot VIVO — jamás pasamos ids arbitrarios al CLI.
func (o *Op) OpHerdr(rw http.ResponseWriter, r *http.Request) {
	b, ok := o.Guard(rw, r)
	if !ok {
		return
	}
	action, id := s(b, "action"), s(b, "id")
	if id == "" {
		fail(rw, 400, "falta el id")
		return
	}
	st := herdr.Snapshot()
	if !st.Available {
		fail(rw, 400, "herdr no está corriendo")
		return
	}
	// validar que el id EXISTA en el snapshot, del tipo correcto
	valid := false
	switch action {
	case "interrupt", "close-pane":
		for _, p := range st.Panes {
			if p.PaneID == id {
				valid = true
			}
		}
	case "close-tab":
		for _, t := range st.Tabs {
			if t.TabID == id {
				valid = true
			}
		}
	case "close-workspace":
		for _, w := range st.Workspaces {
			if w.WorkspaceID == id {
				valid = true
			}
		}
	default:
		fail(rw, 400, "acción desconocida")
		return
	}
	if !valid {
		fail(rw, 404, "ese elemento ya no existe en herdr")
		return
	}
	var err error
	var verb string
	switch action {
	case "interrupt":
		err, verb = herdr.Interrupt(id), "interrumpí (Ctrl-C)"
	case "close-pane":
		err, verb = herdr.ClosePane(id), "cerré la terminal"
	case "close-tab":
		err, verb = herdr.CloseTab(id), "cerré el tab"
	case "close-workspace":
		err, verb = herdr.CloseWorkspace(id), "cerré el workspace"
	}
	if err != nil {
		fail(rw, 500, "herdr no aceptó la acción: "+redact.Clip(err.Error(), 120))
		return
	}
	o.emit("decision", "el humano "+verb+" desde el panel: herdr "+id, "")
	writeJSON(rw, 200, map[string]any{"ok": true, "action": action, "id": id})
}

// OpHerdrKey — respuesta interactiva: manda teclas a un pane (contestar el
// menú de un agente). El pane se valida contra el snapshot vivo; las teclas se
// filtran a un set seguro (dígitos, y/n/s, Enter, flechas, Escape).
func (o *Op) OpHerdrKey(rw http.ResponseWriter, r *http.Request) {
	b, ok := o.Guard(rw, r)
	if !ok {
		return
	}
	pane := s(b, "pane")
	if pane == "" {
		fail(rw, 400, "falta el pane")
		return
	}
	raw, _ := b["keys"].([]any)
	var keys []string
	for _, k := range raw {
		if ks, ok2 := k.(string); ok2 && herdrKeyOK(ks) {
			keys = append(keys, ks)
		}
	}
	if len(keys) == 0 {
		fail(rw, 400, "sin teclas válidas")
		return
	}
	st := herdr.Snapshot()
	if !st.Available {
		fail(rw, 400, "herdr no está corriendo")
		return
	}
	found := false
	for _, p := range st.Panes {
		if p.PaneID == pane {
			found = true
		}
	}
	if !found {
		fail(rw, 404, "ese pane ya no existe")
		return
	}
	if err := herdr.PaneKeys(pane, keys); err != nil {
		fail(rw, 500, "herdr no aceptó las teclas")
		return
	}
	o.emit("decision", "el humano respondió el menú de un agente (herdr "+pane+"): "+strings.Join(keys, " "), "")
	writeJSON(rw, 200, map[string]any{"ok": true})
}

var okKeys = map[string]bool{
	"Enter": true, "Escape": true, "Up": true, "Down": true, "Left": true, "Right": true,
	"Tab": true, "Space": true, "y": true, "n": true, "s": true, "Y": true, "N": true,
}

func herdrKeyOK(k string) bool {
	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
		return true // dígito de menú
	}
	return okKeys[k]
}

func safePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || !strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return "" // solo rutas absolutas sin escapes; vacío = herdr decide
	}
	return p
}

func labelOK(l string) string {
	l = strings.TrimSpace(l)
	if len(l) > 60 {
		l = l[:60]
	}
	out := make([]rune, 0, len(l))
	for _, c := range l {
		if c == '-' || c == '_' || c == ' ' || (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		}
	}
	return string(out)
}

// OpHerdrOpen — abrir cosas en herdr: workspace, terminal (tab), split de pane.
// Persisten por naturaleza de herdr (sobreviven al detach). Guardas + validación.
func (o *Op) OpHerdrOpen(rw http.ResponseWriter, r *http.Request) {
	b, ok := o.Guard(rw, r)
	if !ok {
		return
	}
	action := s(b, "action")
	cwd := safePath(s(b, "cwd"))
	label := labelOK(s(b, "label"))
	st := herdr.Snapshot()
	if !st.Available {
		fail(rw, 400, "herdr no está corriendo")
		return
	}
	var newID string
	var err error
	switch action {
	case "new-workspace":
		newID, err = herdr.WorkspaceCreate(label, cwd)
	case "new-terminal": // un tab nuevo en un workspace
		wsID := s(b, "workspace_id")
		valid := false
		for _, w := range st.Workspaces {
			if w.WorkspaceID == wsID {
				valid = true
			}
		}
		if !valid {
			fail(rw, 404, "ese workspace ya no existe")
			return
		}
		newID, err = herdr.TabCreate(wsID, label, cwd)
	case "split-pane":
		pane := s(b, "pane")
		valid := false
		for _, p := range st.Panes {
			if p.PaneID == pane {
				valid = true
			}
		}
		if !valid {
			fail(rw, 404, "ese pane ya no existe")
			return
		}
		newID, err = herdr.PaneSplit(pane, s(b, "direction"), cwd)
	default:
		fail(rw, 400, "acción desconocida")
		return
	}
	if err != nil {
		fail(rw, 500, "herdr no aceptó: "+redact.Clip(err.Error(), 120))
		return
	}
	o.emit("decision", "el humano abrió desde el panel ("+action+"): "+newID, "")
	writeJSON(rw, 200, map[string]any{"ok": true, "id": newID})
}
