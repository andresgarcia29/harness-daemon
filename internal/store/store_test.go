package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateEsIdempotente(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.db")
	for i := 0; i < 2; i++ { // abrir dos veces = migrar dos veces
		s, err := Open(path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if got := s.schemaVersion(); got != 1 {
			t.Fatalf("schema_version = %d, quiero 1", got)
		}
		s.Close()
	}
}

// La ley 2 del esquema: contar dos veces la misma llamada es IMPOSIBLE.
// Los records repetidos del mismo message.id traen usage acumulándose:
// gana el MÁXIMO por campo, no la suma.
func TestUpsertCallDedupePorMessageID(t *testing.T) {
	s := open(t)
	seed(t, s)
	c := Call{MessageID: "msg-1", SessionID: "sess-1", AgentID: "main",
		Model: "claude-sonnet-5", In: 100, Out: 10, TS: 1000}
	must(t, s.UpsertCall(c))
	c.Out = 50 // el mismo mensaje, más adelante en el stream
	must(t, s.UpsertCall(c))
	c.Out = 30 // un record tardío con MENOS: no debe pisar el máximo
	must(t, s.UpsertCall(c))

	var n, out int64
	must(t, s.DB.QueryRow(`SELECT COUNT(*), MAX(output_tokens) FROM calls`).Scan(&n, &out))
	if n != 1 {
		t.Fatalf("calls = %d, quiero 1 (message_id es PK)", n)
	}
	if out != 50 {
		t.Fatalf("output_tokens = %d, quiero 50 (máximo, no suma ni último)", out)
	}
}

// La ley 1: el costo es una VISTA. Modelo sin precio → cost_usd NULL (jamás
// tarifa por defecto); agregar el precio después re-cotiza el histórico solo.
func TestCostoEsVistaYSinPrecioEsNULL(t *testing.T) {
	s := open(t)
	seed(t, s)
	must(t, s.UpsertCall(Call{MessageID: "m1", SessionID: "sess-1", AgentID: "main",
		Model: "glm-4.7", In: 1_000_000, TS: 1000}))

	var priced int
	var cost *float64
	must(t, s.DB.QueryRow(`SELECT priced, cost_usd FROM call_costs`).Scan(&priced, &cost))
	if priced != 0 || cost != nil {
		t.Fatalf("sin precio: priced=%d cost=%v — el dinero no se inventa", priced, cost)
	}

	_, err := s.DB.Exec(`INSERT INTO prices (model, speed, input, output, source)
		VALUES ('glm-4.7','standard',0.6,2.2,'user')`)
	must(t, err)
	must(t, s.DB.QueryRow(`SELECT priced, cost_usd FROM call_costs`).Scan(&priced, &cost))
	if priced != 1 || cost == nil || *cost < 0.59 || *cost > 0.61 {
		t.Fatalf("con precio: priced=%d cost=%v, quiero ~0.60 — el histórico se re-cotiza solo", priced, cost)
	}
}

func TestEventUIDEstableYConflictoIgnorado(t *testing.T) {
	s := open(t)
	seed(t, s)
	if EventUID("/a/bus.jsonl", 42) != EventUID("/a/bus.jsonl", 42) {
		t.Fatal("EventUID no es determinista")
	}
	if EventUID("/a/bus.jsonl", 42) == EventUID("/a/bus.jsonl", 43) {
		t.Fatal("offsets distintos deben dar uids distintos")
	}
	ok := true
	e := Event{UID: EventUID("x", 1), TS: 10, MachineID: "mach-1",
		WorkspaceID: "ws-1", Kind: "gate", OK: &ok}
	must(t, s.InsertEvent(e))
	must(t, s.InsertEvent(e)) // releer la misma línea: gratis, no error
	var n int64
	must(t, s.DB.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n))
	if n != 1 {
		t.Fatalf("events = %d, quiero 1", n)
	}
}

func TestOffsetsRoundtrip(t *testing.T) {
	s := open(t)
	if o, _ := s.GetOffset("nuevo"); o.Offset != 0 {
		t.Fatalf("fuente nueva debe empezar en 0, fue %d", o.Offset)
	}
	must(t, s.SetOffset("f", Offset{Dev: 1, Ino: 2, Offset: 999}, 123))
	o, err := s.GetOffset("f")
	must(t, err)
	if o.Offset != 999 || o.Ino != 2 {
		t.Fatalf("offset roundtrip: %+v", o)
	}
}

func seed(t *testing.T, s *Store) {
	t.Helper()
	must(t, s.UpsertMachine("mach-1", "h", "darwin", "arm64", "laptop", 1))
	must(t, s.UpsertWorkspace("ws-1", "github.com/x/y", "y", 1))
	must(t, s.UpsertSession("sess-1", "mach-1", "ws-1", "claude-code", 1, 1))
	must(t, s.UpsertAgent("sess-1", "main", "", "orquestador", "", 0, 1, 1))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
