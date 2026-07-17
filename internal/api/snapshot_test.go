package api

import (
	"path/filepath"
	"testing"

	"github.com/andresgarcia29/harness-daemon/internal/store"
)

func TestPeakConcurrency(t *testing.T) {
	cases := []struct {
		name string
		iv   [][2]int64
		want int
	}{
		{"vacío", nil, 0},
		{"un punto", [][2]int64{{100, 100}}, 1},
		{"dos que se solapan", [][2]int64{{0, 10}, {5, 15}}, 2},
		{"tres a la vez", [][2]int64{{0, 30}, {5, 10}, {6, 8}}, 3},
		{"secuenciales sin solape", [][2]int64{{0, 5}, {6, 10}, {11, 15}}, 1},
	}
	for _, c := range cases {
		if got := peakConcurrency(c.iv); got != c.want {
			t.Errorf("%s: peak=%d, quiero %d", c.name, got, c.want)
		}
	}
}

// Build lee del store y arma el snapshot: costo cotizado, dedupe, tiempos
// reales de agente desde las llamadas (no el mtime), pico correcto.
func TestBuildSnapshot(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	must(t, s.SeedBuiltinPrices())
	must(t, s.UpsertMachine("m1", "h", "darwin", "arm64", "laptop", 1))
	must(t, s.UpsertWorkspace("ws1", "github.com/x/y", "y", 1))
	must(t, s.UpsertSession("sess1", "m1", "ws1", "claude-code", 1000, 2000))
	must(t, s.UpsertAgent("sess1", "main", "", "orquestador", "", 0, 1000, 2000))
	must(t, s.UpsertAgent("sess1", "a1", "main", "general-purpose", "Research x", 1, 1000, 2000))
	// intervalos que SE SOLAPAN → pico 2: main [1000,2000], a1 [1200,1800].
	// Cada agente necesita ≥2 llamadas para tener rango (una sola = un punto).
	must(t, s.UpsertCall(store.Call{MessageID: "c1", SessionID: "sess1", AgentID: "main",
		Model: "claude-sonnet-5", In: 1_000_000, Out: 1_000_000, TS: 1000}))
	must(t, s.UpsertCall(store.Call{MessageID: "c1b", SessionID: "sess1", AgentID: "main",
		Model: "claude-sonnet-5", In: 0, Out: 0, TS: 2000}))
	must(t, s.UpsertCall(store.Call{MessageID: "c2", SessionID: "sess1", AgentID: "a1",
		Model: "claude-sonnet-5", In: 1_000_000, Out: 0, TS: 1200}))
	must(t, s.UpsertCall(store.Call{MessageID: "c2b", SessionID: "sess1", AgentID: "a1",
		Model: "claude-sonnet-5", In: 0, Out: 0, TS: 1800}))

	snap, err := Build(s.DB, "ws1", t.TempDir(), 3000)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("sesiones = %d, quiero 1", len(snap.Sessions))
	}
	ss := snap.Sessions[0]
	if ss.NAgents != 2 {
		t.Fatalf("agentes = %d, quiero 2", ss.NAgents)
	}
	if ss.Peak != 2 {
		t.Fatalf("pico = %d, quiero 2 (dos agentes solapados en el tiempo)", ss.Peak)
	}
	if !snap.Op {
		t.Fatal("el daemon debe reportar op:true (el plano de operar vive aquí ahora)")
	}
	// costo total: main (3+15) + a1 (3) = 21
	if snap.Cost == nil || *snap.Cost < 20.9 || *snap.Cost > 21.1 {
		t.Fatalf("costo = %v, quiero ~21", snap.Cost)
	}
	// el agente 'a1' toma su identidad legible de la description
	var found bool
	for _, a := range ss.Agents {
		if a.ID == "a1" && a.Desc == "Research x" && a.Type == "general-purpose" {
			found = true
		}
	}
	if !found {
		t.Fatal("el agente a1 perdió su descripción/tipo")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestBuildSessionHilo(t *testing.T) {
	s, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	must(t, s.SeedBuiltinPrices())
	must(t, s.UpsertMachine("m", "h", "darwin", "arm64", "laptop", 1))
	must(t, s.UpsertWorkspace("ws", "r", "n", 1))
	must(t, s.UpsertSession("S", "m", "ws", "claude-code", 1, 1))
	must(t, s.UpsertAgent("S", "main", "", "orquestador", "", 0, 1, 1))
	must(t, s.UpsertCall(store.Call{MessageID: "c", SessionID: "S", AgentID: "main",
		Model: "claude-sonnet-5", Out: 1_000_000, TS: 100}))
	// el hilo, en desorden de seq → debe salir ordenado ASC
	must(t, s.UpsertThread("S", "main", store.ThreadItem{Seq: 20, TS: 102, Kind: "text", Text: "listo"}))
	must(t, s.UpsertThread("S", "main", store.ThreadItem{Seq: 10, TS: 100, Kind: "think", Text: "pensando"}))
	must(t, s.UpsertThread("S", "main", store.ThreadItem{Seq: 10, TS: 100, Kind: "think", Text: "dup"})) // idempotente
	d, err := BuildSession(s.DB, "S", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Agents) != 1 {
		t.Fatalf("agentes = %d", len(d.Agents))
	}
	th := d.Agents[0].Thread
	if len(th) != 2 {
		t.Fatalf("hilo = %d, quiero 2 (seq 10 dup ignorado)", len(th))
	}
	if th[0].K != "think" || th[1].K != "text" {
		t.Fatalf("hilo desordenado: %s, %s", th[0].K, th[1].K)
	}
}
