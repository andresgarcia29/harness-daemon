package collect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/andresgarcia29/harness-daemon/internal/ident"
	"github.com/andresgarcia29/harness-daemon/internal/store"
)

// fixture arma un workspace + un root de transcripts falsos y devuelve el
// colector listo. Todo temporal, nada de red, nada del workspace real.
func fixture(t *testing.T) (*Collector, string) {
	t.Helper()
	ws := t.TempDir()

	// el bus: 3 eventos reales de forma (uno con ok string, la lección del panel)
	mustMkdir(t, filepath.Join(ws, ".harness"))
	bus := `{"ts":"2026-07-17T01:00:00Z","kind":"phase","task":"COR-1","actor":"harness","summary":"implement — arrancó"}
{"ts":"2026-07-17T01:05:00Z","kind":"gate","task":"COR-1","actor":"atlas","summary":"gate_secrets","ok":"false"}
{"ts":"2026-07-17T01:06:00Z","kind":"ship","task":"COR-1","actor":"atlas","summary":"shippeado","ok":true}
`
	mustWrite(t, filepath.Join(ws, ".harness", "events.jsonl"), bus)

	// una tarea con task.md (frontmatter del panel)
	mustMkdir(t, filepath.Join(ws, "tasks", "COR-1"))
	mustWrite(t, filepath.Join(ws, "tasks", "COR-1", "task.md"),
		"---\nid: COR-1\ntitle: \"Rate limiting\"\norigin: ticket\n---\n")

	// transcripts: CLAUDE_CONFIG_DIR apunta a un root falso con el slug del ws
	cfg := t.TempDir()
	slug := slugRe.ReplaceAllString(ws, "-")
	proj := filepath.Join(cfg, "projects", slug)
	mustMkdir(t, proj)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	sess := "11111111-2222-3333-4444-555555555555"
	// main: 3 records del MISMO message.id con usage acumulándose + 1 sintético
	rec := func(mid, model string, out int64) string {
		b, _ := json.Marshal(map[string]any{
			"type": "assistant", "timestamp": "2026-07-17T01:02:03Z",
			"requestId": "req-1",
			"message": map[string]any{
				"id": mid, "model": model,
				"usage": map[string]any{
					"input_tokens": 10, "output_tokens": out,
					"cache_read_input_tokens": 5,
					"cache_creation": map[string]any{
						"ephemeral_5m_input_tokens": 7, "ephemeral_1h_input_tokens": 3},
				},
			},
		})
		return string(b)
	}
	mustWrite(t, filepath.Join(proj, sess+".jsonl"),
		rec("msg-A", "claude-sonnet-5", 10)+"\n"+
			rec("msg-A", "claude-sonnet-5", 40)+"\n"+
			rec("msg-A", "claude-sonnet-5", 25)+"\n"+ // tardío con menos: no pisa
			rec("msg-S", "<synthetic>", 999)+"\n") // sintético: ni fila ni etiqueta

	// un subagente con meta.json (la identidad legible viene de description)
	subs := filepath.Join(proj, sess, "subagents")
	mustMkdir(t, subs)
	mustWrite(t, filepath.Join(subs, "agent-abc123.jsonl"),
		rec("msg-B", "claude-haiku-4-5", 7)+"\n")
	mustWrite(t, filepath.Join(subs, "agent-abc123.meta.json"),
		`{"agentType":"general-purpose","description":"Research harnesses","spawnDepth":1}`)

	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	m := ident.Machine{ID: "mach-test", OS: "darwin", Arch: "arm64", Kind: "laptop"}
	w := ident.Workspace{ID: "ws-test", Name: "demo", Path: ws}
	return New(st, m, w), sess
}

func counts(t *testing.T, c *Collector) map[string]int64 {
	t.Helper()
	n, err := c.St.Counts()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTickIngestaCompleta(t *testing.T) {
	c, sess := fixture(t)
	if err := c.Tick(1000); err != nil {
		t.Fatal(err)
	}
	n := counts(t, c)
	if n["events"] != 3 {
		t.Fatalf("events = %d, quiero 3", n["events"])
	}
	if n["calls"] != 2 { // msg-A (deduplicado de 3 records) + msg-B; el sintético NO
		t.Fatalf("calls = %d, quiero 2 (dedupe por message_id, sintético fuera)", n["calls"])
	}
	if n["agents"] != 2 { // main + abc123
		t.Fatalf("agents = %d, quiero 2", n["agents"])
	}
	if n["tasks"] != 1 {
		t.Fatalf("tasks = %d, quiero 1", n["tasks"])
	}

	// el usage de msg-A es el MÁXIMO (40), no la suma (75) ni el último (25)
	var out, w5, w1 int64
	must(t, c.St.DB.QueryRow(
		`SELECT output_tokens, cache_write_5m_tokens, cache_write_1h_tokens
		 FROM calls WHERE message_id='msg-A'`).Scan(&out, &w5, &w1))
	if out != 40 {
		t.Fatalf("msg-A out = %d, quiero 40", out)
	}
	if w5 != 7 || w1 != 3 { // el desglose 5m/1h se guarda POR SEPARADO
		t.Fatalf("cache 5m/1h = %d/%d, quiero 7/3", w5, w1)
	}

	// el ok string del hook quedó normalizado a 0
	var okv *int
	must(t, c.St.DB.QueryRow(`SELECT ok FROM events WHERE kind='gate'`).Scan(&okv))
	if okv == nil || *okv != 0 {
		t.Fatalf("gate ok = %v, quiero 0 (string 'false' normalizado en la ingesta)", okv)
	}

	// la fase de COR-1 salió del evento phase
	var phase string
	must(t, c.St.DB.QueryRow(`SELECT phase FROM tasks WHERE id='COR-1'`).Scan(&phase))
	if phase != "implement" {
		t.Fatalf("phase = %q, quiero implement", phase)
	}

	// la identidad legible del subagente viene del meta.json
	var desc string
	must(t, c.St.DB.QueryRow(
		`SELECT description FROM agents WHERE session_id=? AND id='abc123'`, sess).Scan(&desc))
	if desc != "Research harnesses" {
		t.Fatalf("description = %q", desc)
	}
}

// Correr Tick dos veces produce EXACTAMENTE las mismas filas: la idempotencia
// no es una convención del código, es lo que convierte at-least-once en
// exactly-once (ley 2 del esquema).
func TestTickEsIdempotente(t *testing.T) {
	c, _ := fixture(t)
	must(t, c.Tick(1000))
	antes := counts(t, c)
	// segundo tick con offsets en memoria borrados: simula un daemon reiniciado
	// que re-lee todo desde cero
	c.offsets = map[string]int64{}
	c.St.DB.Exec(`DELETE FROM offsets`)
	must(t, c.Tick(2000))
	despues := counts(t, c)
	for tabla, n := range antes {
		if despues[tabla] != n {
			t.Fatalf("%s: %d → %d tras re-ingesta total — la idempotencia se rompió", tabla, n, despues[tabla])
		}
	}
}

// El tail retoma donde iba: líneas nuevas entran, las viejas no se reprocesan
// (y una línea a medio escribir espera a la siguiente pasada).
func TestTailIncremental(t *testing.T) {
	c, _ := fixture(t)
	must(t, c.Tick(1000))
	bus := filepath.Join(c.Workspace.Path, ".harness", "events.jsonl")
	f, _ := os.OpenFile(bus, os.O_APPEND|os.O_WRONLY, 0)
	fmt.Fprintln(f, `{"ts":"2026-07-17T02:00:00Z","kind":"stop","task":"COR-1","summary":"te espero"}`)
	fmt.Fprint(f, `{"ts":"2026-07-17T02:01:00Z","kind":"phase","task":"COR-1","summ`) // a medias
	f.Close()
	must(t, c.Tick(1001))
	n := counts(t, c)
	if n["events"] != 4 { // 3 + el stop; la línea a medias NO
		t.Fatalf("events = %d, quiero 4", n["events"])
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// La lección del VPS: la fase la manda state.json (la verdad ejecutable),
// el bus solo enriquece, los artefactos infieren para tareas legacy, y el
// título cae al heading cuando el frontmatter no lo trae.
func TestFaseVieneDeStateJSONYTituloDelHeading(t *testing.T) {
	c, _ := fixture(t)
	ws := c.Workspace.Path

	// tarea terminada: state.json dice archive aunque el bus diga intake
	done := filepath.Join(ws, "tasks", "AUTO-1-done")
	mustMkdir(t, done)
	mustWrite(t, filepath.Join(done, "task.md"),
		"---\nid: AUTO-1-done\norigin: prompt\n---\n\n# Probar reservas por Telegram\n")
	mustWrite(t, filepath.Join(done, "state.json"), `{"schema":1,"phase":"archive"}`)
	busPath := filepath.Join(ws, ".harness", "events.jsonl")
	old, _ := os.ReadFile(busPath)
	mustWrite(t, busPath, string(old)+
		`{"ts":"2026-07-17T02:00:00Z","kind":"phase","task":"AUTO-1-done","actor":"harness","summary":"intake — nueva tarea"}`+"\n")

	// tarea legacy sin state.json pero con ship.log: los artefactos infieren
	legacy := filepath.Join(ws, "tasks", "AUTO-2-legacy")
	mustMkdir(t, legacy)
	mustWrite(t, filepath.Join(legacy, "task.md"), "---\nid: AUTO-2-legacy\n---\n")
	mustWrite(t, filepath.Join(legacy, "ship.log"), `{"repo":"x"}`)

	if err := c.Tick(1000); err != nil {
		t.Fatal(err)
	}
	var phase, title string
	must(t, c.St.DB.QueryRow(`SELECT phase, title FROM tasks WHERE id='AUTO-1-done'`).Scan(&phase, &title))
	if phase != "archive" {
		t.Fatalf("phase = %q, quiero archive (state.json manda; el bus no la regresa a intake)", phase)
	}
	if title != "Probar reservas por Telegram" {
		t.Fatalf("title = %q, quiero el heading del task.md", title)
	}
	must(t, c.St.DB.QueryRow(`SELECT phase FROM tasks WHERE id='AUTO-2-legacy'`).Scan(&phase))
	if phase != "ship" {
		t.Fatalf("phase legacy = %q, quiero ship (inferida de ship.log)", phase)
	}
}
