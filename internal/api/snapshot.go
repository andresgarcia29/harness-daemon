// Package api construye el snapshot que consume el frontend React — el MISMO
// contrato que sirve el panel de Python, pero leído del SQLite del daemon en
// vez de los archivos. Es la Fase 1: el daemon se vuelve el backend de lectura.
//
// Honesto sobre lo que HOY degrada (se suma en fases siguientes): el daemon es
// solo-lectura (op:false, Operar oculto); no trae aún el texto en vivo por
// agente, el hilo de razonamiento, ni los chips de git — el frontend cae con
// gracia a sus vacíos cuando esos campos faltan.
package api

import (
	"database/sql"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/store"
)

const activeWindow = 90 // s sin actividad → ya no "activo"

type usage struct {
	In    int64 `json:"in"`
	Out   int64 `json:"out"`
	CRead int64 `json:"cache_read"`
	CCrea int64 `json:"cache_creation"`
}
type agent struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Desc    string   `json:"desc"`
	Model   string   `json:"model"`
	Active  bool     `json:"active"`
	FirstTS int64    `json:"first_ts"`
	LastTS  int64    `json:"last_ts"`
	Idle    int64    `json:"idle"`
	Elapsed int64    `json:"elapsed"`
	Usage   usage    `json:"usage"`
	Cost    *float64 `json:"cost"`
	Depth   int      `json:"depth"`
}
type session struct {
	ID      string   `json:"id"`
	Short   string   `json:"short"`
	Model   string   `json:"model"`
	NAgents int      `json:"n_agents"`
	NActive int      `json:"n_active"`
	Peak    int      `json:"peak"`
	Idle    int64    `json:"idle"`
	Tokens  tokens   `json:"tokens"`
	Cost    *float64 `json:"cost"`
	Agents  []agent  `json:"agents"`
	// LiveBy dice CÓMO sabemos que está viva: "herdr" = una terminal de herdr la
	// corre AHORA (verdad de campo); "recent" = el transcript se tocó hace poco
	// pero herdr no lo confirma (terminó hace nada o corre fuera de herdr);
	// "" = en reposo. Lo llena EnrichLiveness. El mtime ya no miente "trabajando".
	LiveBy string `json:"live_by"`
	// cwd de la sesión: ancla su tarea (worktrees/<id>/) y se cruza con pane.cwd
	// de herdr. Ahora expuesto para agrupar sesiones dentro de su tarea.
	Cwd string `json:"cwd,omitempty"`
}
type tokens struct {
	Out   int64 `json:"out"`
	CRead int64 `json:"cache_read"`
}
type event struct {
	TS      string `json:"ts"`
	Kind    string `json:"kind"`
	Task    string `json:"task"`
	Actor   string `json:"actor"`
	Summary string `json:"summary"`
	OK      *bool  `json:"ok,omitempty"`
}
type task struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Origin      string   `json:"origin"`
	Phase       string   `json:"phase"`
	Done        []string `json:"done"`
	Verdicts    verdicts `json:"verdicts"`
	Assumptions []string `json:"assumptions"`
}
type verdicts struct {
	Pass  int `json:"pass"`
	Total int `json:"total"`
}
type dayCost struct {
	Day      string             `json:"day"`
	Cost     float64            `json:"cost"`
	Out      int64              `json:"out"`
	Unpriced bool               `json:"unpriced"`
	ByModel  map[string]float64 `json:"by_model"`
}
type modelCost struct {
	Model string   `json:"model"`
	In    int64    `json:"in"`
	Out   int64    `json:"out"`
	CRead int64    `json:"cache_read"`
	CCrea int64    `json:"cache_creation"`
	Cost  *float64 `json:"cost"`
}
type price struct {
	Input, Output, CRead, CW5m, CW1h float64
}

// Snapshot es el objeto que /api/state y /api/stream devuelven.
type Snapshot struct {
	TS            int64            `json:"ts"`
	Sessions      []session        `json:"sessions"`
	Events        []event          `json:"events"`
	Tasks         []task           `json:"tasks"`
	Tokens        tokens           `json:"tokens"`
	Cost          *float64         `json:"cost"`
	Days          []dayCost        `json:"days"`
	Models        []modelCost      `json:"models"`
	Prices        map[string]pubP  `json:"prices"`
	Unpriced      []string         `json:"unpriced"`
	Connections   map[string]bool  `json:"connections"`
	Runs          []map[string]any `json:"runs"`
	Mode          string           `json:"mode"`
	Op            bool             `json:"op"`
	Workspace     wsInfo           `json:"workspace"`
	Toolbox       *Toolbox         `json:"toolbox,omitempty"`
	Mcp           []McpServer      `json:"mcp"`
	Herdr         any              `json:"herdr,omitempty"`
	Targets       []Target         `json:"targets"`        // máquinas remotas (VPS) por SSH
	ArchivedTasks []string         `json:"archived_tasks"` // tareas ocultas (el bus las revive si no se excluyen)
	Warning       string           `json:"warning,omitempty"`
	// Init es el estado del wizard de onboarding (initflow.PublicState) cuando
	// hay un init activo; `any` para no crear ciclo de imports con initflow.
	Init any `json:"init,omitempty"`
}
type wsInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type pubP struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

func costOf(p price, u usage, ok bool) *float64 {
	if !ok {
		return nil
	}
	c := (float64(u.In)*p.Input + float64(u.Out)*p.Output +
		float64(u.CRead)*p.CRead + float64(u.CCrea)*p.CW5m) / 1e6
	return &c
}

// EmptySnapshot devuelve un snapshot vacío pero con todas las listas/mapas
// inicializados (el frontend nunca ve null). Para el fallback cuando no se pudo
// traer el snapshot de un VPS — muestra vacío + un warning, no rompe la vista.
func EmptySnapshot() *Snapshot {
	return &Snapshot{
		Mode: "daemon", Op: true,
		Sessions: []session{}, Events: []event{}, Tasks: []task{},
		Days: []dayCost{}, Models: []modelCost{}, Prices: map[string]pubP{},
		Unpriced: []string{}, Runs: []map[string]any{}, Connections: map[string]bool{},
		Mcp: []McpServer{}, ArchivedTasks: []string{}, Targets: []Target{},
	}
}

// Build arma el snapshot de UN workspace desde el store. wsPath es la ruta
// local del workspace (para Docs/Skills, que se leen de sus archivos).
func Build(db store.Queryer, workspaceID, wsPath string, now int64) (*Snapshot, error) {
	prices, _ := loadPrices(db)
	snap := &Snapshot{
		TS: now, Mode: "daemon", Op: true,
		Sessions: []session{}, Events: []event{}, Tasks: []task{},
		Days: []dayCost{}, Models: []modelCost{}, Prices: map[string]pubP{},
		Unpriced: []string{}, Runs: []map[string]any{}, Connections: map[string]bool{},
		Toolbox: BuildToolbox(wsPath), Mcp: BuildMcp(wsPath),
		Workspace: wsInfo{Path: wsPath}, ArchivedTasks: []string{},
	}
	for m, p := range prices {
		snap.Prices[m] = pubP{Input: p.Input, Output: p.Output}
	}

	// ── agregados de calls por (sesión, agente) ──
	type key struct{ s, a string }
	au := map[key]*usage{}
	amodel := map[key]string{}
	amodelTS := map[key]int64{}
	// primer/último ts REAL de cada agente = min/max de SUS llamadas. El
	// colector guarda started/last_seen como el mtime del archivo (un punto),
	// que aplana la línea de tiempo y el pico de concurrencia — el reloj bueno
	// es el de los records, jamás el del archivo.
	aFirst := map[key]int64{}
	aLast := map[key]int64{}
	dayAgg := map[string]*dayCost{}
	modAgg := map[string]*modelCost{}
	unpriced := map[string]bool{}
	var totOut, totCRead int64
	var totCost float64

	rows, err := db.Query(`
		SELECT c.session_id, c.agent_id, c.model, c.input_tokens, c.output_tokens,
		       c.cache_read_tokens, c.cache_write_5m_tokens, c.cache_write_1h_tokens, c.ts
		FROM calls c JOIN sessions s ON s.id = c.session_id
		WHERE s.workspace_id = ?`, workspaceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sid, aid, model string
		var in, out, cr, cw5, cw1, ts int64
		if rows.Scan(&sid, &aid, &model, &in, &out, &cr, &cw5, &cw1, &ts) != nil {
			continue
		}
		k := key{sid, aid}
		if au[k] == nil {
			au[k] = &usage{}
		}
		u := au[k]
		u.In += in
		u.Out += out
		u.CRead += cr
		u.CCrea += cw5 + cw1
		if ts >= amodelTS[k] {
			amodelTS[k] = ts
			amodel[k] = model
		}
		if ts > 0 {
			if aFirst[k] == 0 || ts < aFirst[k] {
				aFirst[k] = ts
			}
			if ts > aLast[k] {
				aLast[k] = ts
			}
		}
		// agregado por día × modelo
		day := time.Unix(ts, 0).UTC().Format("2006-01-02")
		if dayAgg[day] == nil {
			dayAgg[day] = &dayCost{Day: day, ByModel: map[string]float64{}}
		}
		p, ok := prices[model]
		cu := usage{In: in, Out: out, CRead: cr, CCrea: cw5 + cw1}
		c := costOf(p, cu, ok)
		dayAgg[day].Out += out
		if c == nil {
			dayAgg[day].Unpriced = true
			unpriced[model] = true
		} else {
			dayAgg[day].Cost += *c
			dayAgg[day].ByModel[model] += *c
			totCost += *c
		}
		// agregado por modelo
		if modAgg[model] == nil {
			modAgg[model] = &modelCost{Model: model}
		}
		ma := modAgg[model]
		ma.In += in
		ma.Out += out
		ma.CRead += cr
		ma.CCrea += cw5 + cw1
		totOut += out
		totCRead += cr
	}
	rows.Close()

	// ── sesiones + agentes ──
	sr, err := db.Query(`SELECT id, last_seen, cwd FROM sessions WHERE workspace_id = ? AND archived = 0 ORDER BY last_seen DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	var sessIDs []string
	sessLast := map[string]int64{}
	sessCwd := map[string]string{}
	for sr.Next() {
		var id, cwd string
		var last int64
		if sr.Scan(&id, &last, &cwd) == nil {
			sessIDs = append(sessIDs, id)
			sessLast[id] = last
			sessCwd[id] = cwd
		}
	}
	sr.Close()

	for _, sid := range sessIDs {
		ar, err := db.Query(`SELECT id, type, description, spawn_depth, started, last_seen
			FROM agents WHERE session_id = ? ORDER BY started`, sid)
		if err != nil {
			continue
		}
		var agents []agent
		var intervals [][2]int64
		var sOut, sCRead int64
		var sCost float64
		var sCostKnown bool
		mainModel := ""
		for ar.Next() {
			var aid, typ, desc string
			var depth int
			var started, last int64
			if ar.Scan(&aid, &typ, &desc, &depth, &started, &last) != nil {
				continue
			}
			k := key{sid, aid}
			u := usage{}
			if au[k] != nil {
				u = *au[k]
			}
			model := amodel[k]
			p, ok := prices[model]
			c := costOf(p, u, ok)
			// tiempos reales de las llamadas; si el agente no tiene, cae al
			// started/last_seen del archivo (un hecho medido, no inventado).
			first, lst := aFirst[k], aLast[k]
			if first == 0 {
				first = started
			}
			if lst == 0 {
				lst = last
			}
			active := lst > 0 && now-lst < activeWindow
			el := int64(0)
			if first > 0 && lst > 0 {
				el = lst - first
			}
			ag := agent{
				ID: aid, Type: typ, Desc: desc, Model: model, Active: active,
				FirstTS: first, LastTS: lst, Idle: now - lst, Elapsed: el,
				Usage: u, Cost: c, Depth: depth,
			}
			agents = append(agents, ag)
			if first > 0 && lst > 0 {
				intervals = append(intervals, [2]int64{first, lst})
			}
			sOut += u.Out
			sCRead += u.CRead
			if c != nil {
				sCost += *c
				sCostKnown = true
			}
			if aid == "main" {
				mainModel = model
			}
		}
		ar.Close()
		nActive := 0
		for _, a := range agents {
			if a.Active {
				nActive++
			}
		}
		var scost *float64
		if sCostKnown {
			scost = &sCost
		}
		snap.Sessions = append(snap.Sessions, session{
			ID: sid, Short: short(sid), Model: mainModel, NAgents: len(agents),
			NActive: nActive, Peak: peakConcurrency(intervals), Idle: now - sessLast[sid],
			Tokens: tokens{Out: sOut, CRead: sCRead}, Cost: scost, Agents: agents,
			Cwd: sessCwd[sid],
		})
	}

	// ── eventos del bus ──
	er, err := db.Query(`SELECT ts, kind, COALESCE(task_id,''), COALESCE(actor,''),
		COALESCE(summary,''), ok FROM events WHERE workspace_id = ? ORDER BY ts DESC LIMIT 200`, workspaceID)
	if err == nil {
		var evs []event
		for er.Next() {
			var ts int64
			var kind, tid, actor, summary string
			var ok sql.NullInt64
			if er.Scan(&ts, &kind, &tid, &actor, &summary, &ok) != nil {
				continue
			}
			e := event{TS: time.Unix(ts, 0).UTC().Format(time.RFC3339), Kind: kind,
				Task: tid, Actor: actor, Summary: summary}
			if ok.Valid {
				b := ok.Int64 != 0
				e.OK = &b
			}
			evs = append(evs, e)
		}
		er.Close()
		// el frontend espera orden ascendente (lo más viejo primero)
		for i := len(evs) - 1; i >= 0; i-- {
			snap.Events = append(snap.Events, evs[i])
		}
	}

	// ── tareas ──
	tr, err := db.Query(`SELECT id, COALESCE(title,''), COALESCE(origin,''), COALESCE(phase,'')
		FROM tasks WHERE workspace_id = ? AND archived = 0 ORDER BY last_seen DESC`, workspaceID)
	if err == nil {
		for tr.Next() {
			var id, title, origin, phase string
			if tr.Scan(&id, &title, &origin, &phase) == nil {
				snap.Tasks = append(snap.Tasks, task{
					ID: id, Title: title, Origin: origin, Phase: phase,
					Done: []string{}, Assumptions: []string{},
				})
			}
		}
		tr.Close()
	}
	// ids de tareas archivadas: el frontend las excluye de la unión con los
	// eventos del bus (que si no, las revivirían en la lista).
	if ar, e := db.Query(`SELECT id FROM tasks WHERE workspace_id = ? AND archived = 1`, workspaceID); e == nil {
		for ar.Next() {
			var id string
			if ar.Scan(&id) == nil {
				snap.ArchivedTasks = append(snap.ArchivedTasks, id)
			}
		}
		ar.Close()
	}

	// ── días, modelos, totales ──
	for _, d := range sortedDays(dayAgg) {
		snap.Days = append(snap.Days, *d)
	}
	for _, m := range modAgg {
		p, ok := prices[m.Model]
		m.Cost = costOf(p, usage{In: m.In, Out: m.Out, CRead: m.CRead, CCrea: m.CCrea}, ok)
		snap.Models = append(snap.Models, *m)
	}
	for u := range unpriced {
		snap.Unpriced = append(snap.Unpriced, u)
	}
	snap.Tokens = tokens{Out: totOut, CRead: totCRead}
	if len(snap.Models) > 0 {
		snap.Cost = &totCost
	}
	return snap, nil
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func loadPrices(db store.Queryer) (map[string]price, error) {
	out := map[string]price{}
	rows, err := db.Query(`SELECT model, input, output,
		COALESCE(cache_read,0), COALESCE(cache_write_5m,0), COALESCE(cache_write_1h,0)
		FROM prices WHERE valid_from = 0`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		var p price
		if rows.Scan(&m, &p.Input, &p.Output, &p.CRead, &p.CW5m, &p.CW1h) == nil {
			out[m] = p
		}
	}
	return out, nil
}

// peakConcurrency: máximo de intervalos [ini,fin] que se solapan a la vez.
func peakConcurrency(iv [][2]int64) int {
	if len(iv) == 0 {
		return 0
	}
	type pt struct {
		t int64
		d int
	}
	pts := make([]pt, 0, len(iv)*2)
	for _, x := range iv {
		pts = append(pts, pt{x[0], 1}, pt{x[1], -1})
	}
	// orden por tiempo; a igualdad, las ENTRADAS (+1) antes que las salidas (-1),
	// para que un intervalo-punto (ini==fin) cuente como 1 y los que se tocan se
	// cuenten solapados (si no, el pico da 0).
	for i := 1; i < len(pts); i++ {
		for j := i; j > 0 && (pts[j].t < pts[j-1].t || (pts[j].t == pts[j-1].t && pts[j].d > pts[j-1].d)); j-- {
			pts[j], pts[j-1] = pts[j-1], pts[j]
		}
	}
	cur, peak := 0, 0
	for _, p := range pts {
		cur += p.d
		if cur > peak {
			peak = cur
		}
	}
	return peak
}

func sortedDays(m map[string]*dayCost) []*dayCost {
	out := make([]*dayCost, 0, len(m))
	for _, d := range m {
		out = append(out, d)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Day < out[j-1].Day; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
