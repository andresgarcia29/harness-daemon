// Package collect es el colector (Fase 3): lee las dos fuentes y las mete al
// almacén con exactly-once en el resultado.
//
//   - El bus del harness (.harness/events.jsonl + tasks/) — NUESTRO, estable.
//   - Los transcripts de Claude Code — PRESTADOS: formato interno, cambia entre
//     versiones. Si su parseo falla, el bus sigue entrando; nunca al revés.
//
// Las leyes vienen del panel Python, donde cada una costó un bug real:
//   - EL RELOJ ES EL DEL RECORD, JAMÁS EL NUESTRO. Un record sin timestamp no
//     toca relojes; time.Now() jamás se estampa como dato (3 incidentes).
//   - DEDUPE POR message.id: los records repiten el usage acumulado de la misma
//     respuesta; nos quedamos con el MÁXIMO por campo (medido: 1.01× de inflado).
//   - <synthetic> no es facturable y no etiqueta a nadie.
//   - El desglose de caché 5m/1h gana sobre el campo plano (1.25× vs 2×) — y
//     aquí, a diferencia del panel, el esquema los guarda por separado.
package collect

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/ident"
	"github.com/andresgarcia29/harness-daemon/internal/redact"
	"github.com/andresgarcia29/harness-daemon/internal/store"
)

const maxSessions = 12 // las más recientes; igual que el panel

type Collector struct {
	St         *store.Store
	Machine    ident.Machine
	Workspace  ident.Workspace
	projectDir string // transcripts de Claude Code para este workspace ("" = aún no hallado)
	offsets    map[string]int64
	cwdDone    map[string]bool // sesiones cuyo cwd ya guardamos (se lee una vez)
}

func New(st *store.Store, m ident.Machine, w ident.Workspace) *Collector {
	return &Collector{St: st, Machine: m, Workspace: w, offsets: map[string]int64{}, cwdDone: map[string]bool{}}
}

// Tick corre una pasada completa de ingesta. Idempotente por construcción:
// correrlo dos veces produce exactamente las mismas filas.
func (c *Collector) Tick(now int64) error {
	// identidad primero: todo lo demás la referencia (FKs)
	if err := c.St.UpsertMachine(c.Machine.ID, c.Machine.Hostname, c.Machine.OS,
		c.Machine.Arch, c.Machine.Kind, now); err != nil {
		return err
	}
	if err := c.St.UpsertWorkspace(c.Workspace.ID, c.Workspace.Remote, c.Workspace.Name, now); err != nil {
		return err
	}
	_ = c.St.UpsertWorkspacePath(c.Machine.ID, c.Workspace.ID, c.Workspace.Path, now)

	c.scanBus()
	c.scanTasks(now)
	c.scanTranscripts(now) // prestado: sus errores no tumban el tick
	return nil
}

// ── Fuente 1: el bus (nuestro) ───────────────────────────────────────────

func (c *Collector) scanBus() {
	path := filepath.Join(c.Workspace.Path, ".harness", "events.jsonl")
	c.tail(path, func(line []byte, off int64) {
		var rec struct {
			TS      string          `json:"ts"`
			Kind    string          `json:"kind"`
			Task    string          `json:"task"`
			Actor   string          `json:"actor"`
			Summary string          `json:"summary"`
			OK      json.RawMessage `json:"ok"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Kind == "" {
			return // línea rota: se salta, no se muere
		}
		e := store.Event{
			UID: store.EventUID(path, off), TS: parseTS(rec.TS),
			MachineID: c.Machine.ID, WorkspaceID: c.Workspace.ID,
			TaskID: rec.Task, Kind: rec.Kind, Actor: rec.Actor, Summary: rec.Summary,
		}
		// ok llega como bool del bus y como string de los hooks (la lección
		// del panel: normalizar EN LA INGESTA, no en cada consumidor)
		switch strings.Trim(string(rec.OK), `"`) {
		case "true":
			t := true
			e.OK = &t
		case "false":
			f := false
			e.OK = &f
		}
		_ = c.St.InsertEvent(e)
		// un evento de fase actualiza la fase de su tarea
		if rec.Kind == "phase" && rec.Task != "" {
			if ph := phaseOf(rec.Summary); ph != "" {
				// enriquecimiento: solo si la tarea no tiene state.json (la
				// verdad ejecutable); si lo tiene, scanTasks ya mandó la fase
				// real y un evento viejo no debe pisarla
				if statePhase(filepath.Join(c.Workspace.Path, "tasks", rec.Task)) == "" {
					_ = c.St.UpsertTask(c.Workspace.ID, rec.Task, "", "", ph, e.TS)
				}
			}
		}
	})
}

var phaseRe = regexp.MustCompile(`^(intake|rfc|implement|review|ship|deploy|archive)\b`)

func phaseOf(summary string) string {
	return phaseRe.FindString(strings.ToLower(strings.TrimSpace(summary)))
}

func (c *Collector) scanTasks(now int64) {
	dirs, _ := filepath.Glob(filepath.Join(c.Workspace.Path, "tasks", "*"))
	for _, d := range dirs {
		md := filepath.Join(d, "task.md")
		b, err := os.ReadFile(md)
		if err != nil {
			continue
		}
		id, title, origin := filepath.Base(d), "", ""
		for _, ln := range strings.Split(string(b), "\n") {
			switch {
			case strings.HasPrefix(ln, "id: "):
				id = strings.TrimSpace(ln[4:])
			case strings.HasPrefix(ln, "title: "):
				title = strings.Trim(strings.TrimSpace(ln[7:]), `"`)
			case strings.HasPrefix(ln, "origin: "):
				origin = strings.TrimSpace(ln[8:])
			case ln == "---" && title != "":
				break
			}
		}
		if title == "" {
			// /auto escribe el título como heading del cuerpo, no como
			// frontmatter: sin este fallback, toda tarea origin:prompt
			// aparecía como "Sin título registrado" (visto en el VPS).
			for _, ln := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(ln, "# ") {
					title = strings.TrimSpace(strings.Trim(ln[2:], "`"))
					break
				}
			}
		}
		st, _ := os.Stat(md)
		ts := now
		if st != nil {
			ts = st.ModTime().Unix() // el mtime es un hecho; now es el fallback
		}
		// LA FASE VIENE DE state.json (la verdad ejecutable de harness-policy),
		// jamás solo del bus: una tarea en archive se quedaba pintada en
		// intake si el orquestador no emitió los eventos de fase (visto en el
		// VPS). El bus queda como enriquecimiento; los artefactos, como
		// inferencia para tareas legacy sin state.json.
		phase := statePhase(d)
		if phase == "" {
			phase = phaseFromArtifacts(d)
		}
		_ = c.St.UpsertTask(c.Workspace.ID, id, title, origin, phase, ts)
	}
}

// statePhase — la fase de tasks/<id>/state.json (vacía si no existe o no parsea).
func statePhase(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return ""
	}
	var st struct {
		Phase string `json:"phase"`
	}
	if json.Unmarshal(b, &st) != nil {
		return ""
	}
	return st.Phase
}

// phaseFromArtifacts — inferencia para tareas anteriores al motor de estados:
// el artefacto más avanzado presente define la fase (mismo principio que el
// resume de /auto: los artefactos SON el estado).
func phaseFromArtifacts(dir string) string {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case has("ship.log"):
		return "ship"
	case hasGlob(dir, "verdict-*.json"):
		return "review"
	case has("plan.md"):
		return "implement"
	case has("assumptions.md"):
		return "rfc"
	}
	// sin artefactos que prueben fase: vacio, para que el bus (enriquecimiento)
	// pueda aportar la suya sin que la inferencia lo pise
	return ""
}

func hasGlob(dir, pat string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, pat))
	return len(m) > 0
}

// ── Fuente 2: transcripts de Claude Code (prestados) ─────────────────────

// candidateRoots: el root NO es siempre ~/.claude — CLAUDE_CONFIG_DIR lo mueve
// (perfiles). Asumirlo es el bug más fácil de cometer aquí.
func candidateRoots() []string {
	var roots []string
	if env := os.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		for _, p := range strings.Split(env, ":") {
			if p = strings.TrimSpace(p); p != "" {
				roots = append(roots, p)
			}
		}
	}
	home, _ := os.UserHomeDir()
	roots = append(roots, filepath.Join(home, ".claude"), filepath.Join(home, ".config", "claude"))
	if sub, err := filepath.Glob(filepath.Join(home, ".claude", "*")); err == nil {
		sort.Strings(sub)
		roots = append(roots, sub...)
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range roots {
		p := filepath.Join(r, "projects")
		if !seen[p] {
			seen[p] = true
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				out = append(out, p)
			}
		}
	}
	return out
}

var slugRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

func findProjectDir(workspace string) string {
	slug := slugRe.ReplaceAllString(workspace, "-")
	for _, projects := range candidateRoots() {
		guess := filepath.Join(projects, slug)
		if fi, err := os.Stat(guess); err == nil && fi.IsDir() {
			return guess
		}
	}
	// slug sin match: confirmar por el campo cwd de un transcript real
	for _, projects := range candidateRoots() {
		dirs, _ := filepath.Glob(filepath.Join(projects, "*"))
		sort.Strings(dirs)
		for _, d := range dirs {
			files, _ := filepath.Glob(filepath.Join(d, "*.jsonl"))
			sort.Strings(files)
			for i, f := range files {
				if i >= 2 {
					break
				}
				if firstCwd(f) == workspace {
					return d
				}
			}
		}
	}
	return ""
}

// firstCwd devuelve el primer `cwd` que aparece en el transcript. NO basta con
// la primera línea: Claude Code arranca con records `queue-operation` sin cwd;
// el cwd sale unas líneas después (en `attachment`/mensajes). Escaneamos las
// primeras ~60 líneas y paramos al primero — barato y robusto.
func firstCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // records grandes (adjuntos)
	for i := 0; sc.Scan() && i < 60; i++ {
		var rec struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Cwd != "" {
			return rec.Cwd
		}
	}
	return ""
}

func (c *Collector) scanTranscripts(now int64) {
	defer func() { _ = recover() }() // formato prestado: si cambia, el bus sigue
	if c.projectDir == "" {
		c.projectDir = findProjectDir(c.Workspace.Path)
		if c.projectDir == "" {
			return
		}
	}
	files, _ := filepath.Glob(filepath.Join(c.projectDir, "*.jsonl"))
	sort.Slice(files, func(i, j int) bool { return mtime(files[i]) > mtime(files[j]) })
	if len(files) > maxSessions {
		files = files[:maxSessions]
	}
	for _, f := range files {
		sid := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		_ = c.St.UpsertSession(sid, c.Machine.ID, c.Workspace.ID, "claude-code",
			mtime(f), mtime(f))
		// cwd de la sesión (una vez): ancla para cruzarla con las terminales de
		// herdr y saber si de verdad corre — sin adivinar por mtime.
		if !c.cwdDone[sid] {
			if cw := firstCwd(f); cw != "" {
				_ = c.St.SetSessionCwd(sid, cw)
				c.cwdDone[sid] = true // sólo al lograrlo: reintenta si aún no hay cwd
			}
		}
		_ = c.St.UpsertAgent(sid, "main", "", "orquestador", "", 0, mtime(f), mtime(f))
		c.tail(f, func(line []byte, off int64) { c.ingest(line, sid, "main", off) })

		subs, _ := filepath.Glob(filepath.Join(c.projectDir, sid, "subagents", "agent-*.jsonl"))
		for _, af := range subs {
			aid := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(af), "agent-"), ".jsonl")
			typ, desc, depth := "", "", 1
			if mb, err := os.ReadFile(strings.TrimSuffix(af, ".jsonl") + ".meta.json"); err == nil {
				var meta struct {
					AgentType   string `json:"agentType"`
					Description string `json:"description"`
					SpawnDepth  int    `json:"spawnDepth"`
				}
				if json.Unmarshal(mb, &meta) == nil {
					typ, desc = meta.AgentType, meta.Description
					if meta.SpawnDepth > 0 {
						depth = meta.SpawnDepth
					}
				}
			}
			_ = c.St.UpsertAgent(sid, aid, "main", typ, desc, depth, mtime(af), mtime(af))
			c.tail(af, func(line []byte, off int64) { c.ingest(line, sid, aid, off) })
		}
	}
}

// ingest procesa UN record de transcript. Solo los hechos facturables.
func (c *Collector) ingest(line []byte, sid, aid string, off int64) {
	var rec struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		RequestID string `json:"requestId"`
		Message   struct {
			ID      string          `json:"id"`
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
			Usage   struct {
				In        int64 `json:"input_tokens"`
				Out       int64 `json:"output_tokens"`
				CacheRead int64 `json:"cache_read_input_tokens"`
				CacheFlat int64 `json:"cache_creation_input_tokens"`
				Cache     struct {
					E5m int64 `json:"ephemeral_5m_input_tokens"`
					E1h int64 `json:"ephemeral_1h_input_tokens"`
				} `json:"cache_creation"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" {
		return
	}
	m := rec.Message
	if m.ID == "" || m.Model == "" || m.Model == "<synthetic>" {
		return // sintético o sin id: no es facturable ni razonamiento real
	}
	w5, w1 := m.Usage.Cache.E5m, m.Usage.Cache.E1h
	if w5 == 0 && w1 == 0 && m.Usage.CacheFlat > 0 {
		w5 = m.Usage.CacheFlat // solo el plano: asumimos 5m (el barato — honesto)
	}
	ts := parseTS(rec.Timestamp) // 0 si no hay: el reloj es el del record o nada
	_ = c.St.UpsertCall(store.Call{
		MessageID: m.ID, RequestID: rec.RequestID, SessionID: sid, AgentID: aid,
		Model: m.Model, In: m.Usage.In, Out: m.Usage.Out,
		CacheRead: m.Usage.CacheRead, CacheWrite5m: w5, CacheWrite1h: w1, TS: ts,
	})

	// El hilo de razonamiento: texto, pensamiento y herramientas. seq estable =
	// offset del byte × 100 + índice de bloque → idempotente al re-leer. TODO
	// texto va REDACTADO antes del disco (la ley de secretos también aplica a
	// lo que se persiste, no solo a lo que se muestra).
	var blocks []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	}
	if len(m.Content) == 0 || json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	for i, b := range blocks {
		it := store.ThreadItem{Seq: off*100 + int64(i), TS: ts}
		switch b.Type {
		case "text":
			if b.Text == "" {
				continue
			}
			it.Kind, it.Text = "text", redact.Clip(b.Text, 2000)
		case "thinking":
			if b.Thinking == "" {
				continue
			}
			it.Kind, it.Text = "think", redact.Clip(b.Thinking, 2000)
		case "tool_use":
			it.Kind, it.Text, it.Hint = "tool", b.Name, toolHint(b.Input)
		default:
			continue
		}
		_ = c.St.UpsertThread(sid, aid, it)
	}
}

// toolHint: una pista corta de qué hizo una herramienta, redactada.
func toolHint(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range []string{"file_path", "path", "pattern", "command", "url", "query", "description"} {
		if v, ok := m[k].(string); ok && v != "" {
			return redact.Clip(v, 120)
		}
	}
	return ""
}

// ── tail: lector incremental con offset persistido ───────────────────────
//
// El offset vive en la DB (tabla offsets): un daemon que se reinicia retoma
// donde iba, no re-lee gigas de transcripts. size < offset = truncado/rotado →
// desde cero (los UPSERTs hacen que re-leer sea gratis, no un bug).
func (c *Collector) tail(path string, fn func(line []byte, off int64)) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	off := c.offsets[path]
	if off == 0 {
		if o, err := c.St.GetOffset(path); err == nil {
			off = o.Offset
		}
	}
	if fi.Size() < off {
		off = 0
	}
	if fi.Size() == off {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(off, 0); err != nil {
		return
	}
	buf := make([]byte, fi.Size()-off)
	n, _ := f.Read(buf)
	buf = buf[:n]
	for len(buf) > 0 {
		i := strings.IndexByte(string(buf), '\n')
		if i < 0 {
			break // línea a medio escribir: la próxima pasada la termina
		}
		line := buf[:i]
		if len(strings.TrimSpace(string(line))) > 0 {
			fn(line, off)
		}
		off += int64(i) + 1
		buf = buf[i+1:]
	}
	c.offsets[path] = off
	_ = c.St.SetOffset(path, store.Offset{Offset: off}, time.Now().Unix())
}

func mtime(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.ModTime().Unix()
}

func parseTS(iso string) int64 {
	if iso == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, iso); err == nil {
			return t.Unix()
		}
	}
	return 0
}
