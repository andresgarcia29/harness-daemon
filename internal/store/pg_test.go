package store

import (
	"os"
	"testing"
)

// Prueba de integración Postgres. NO corre en `go test` normal: exige un DSN de
// un Postgres DESECHABLE en HARNESS_PG_DSN (el test hace DROP SCHEMA public para
// empezar limpio y ser re-ejecutable — jamás lo apuntes a datos que te importen).
//
//	createdb harness_test
//	HARNESS_PG_DSN='postgres://localhost:5432/harness_test?sslmode=disable' \
//	  go test ./internal/store/ -run TestPostgres -v
//
// Es el MISMO contrato que store_test.go (que corre en SQLite): si las tres leyes
// del esquema se cumplen igual sobre Postgres, el port es correcto. Verifica
// además lo que SÓLO puede fallar en PG: el rebind ?→$N, BIGINT que no desborda,
// la vista con priced entero, y la columna reservada "offset".
func openPG(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("HARNESS_PG_DSN")
	if dsn == "" {
		t.Skip("sin HARNESS_PG_DSN — prueba de integración Postgres omitida")
	}
	// arrancar de cero: el esquema completo se re-crea vía migraciones.
	pre, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open (limpieza): %v", err)
	}
	if _, err := pre.DB.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		pre.Close()
		t.Fatalf("no pude limpiar el schema public: %v", err)
	}
	pre.Close()

	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if s.Dialect != "postgres" {
		t.Fatalf("Dialect = %q, quiero postgres", s.Dialect)
	}
	return s
}

func TestPostgresMigraYVersiona(t *testing.T) {
	s := openPG(t)
	if v := s.schemaVersion(); v < 4 {
		t.Fatalf("schema_version = %d, quiero >= 4 (hay 004_archive)", v)
	}
	// re-abrir NO debe re-migrar (idempotente).
	s2, err := Open(os.Getenv("HARNESS_PG_DSN"))
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer s2.Close()
	if s2.schemaVersion() != s.schemaVersion() {
		t.Fatalf("re-abrir migró de más")
	}
}

// La ley 2 sobre Postgres: message_id es PK, gana el MÁXIMO por campo. Ejercita
// el UPSERT con GREATEST (no MAX escalar) y el rebind de placeholders.
func TestPostgresUpsertCallDedupe(t *testing.T) {
	s := openPG(t)
	seed(t, s)
	c := Call{MessageID: "msg-1", SessionID: "sess-1", AgentID: "main",
		Model: "claude-sonnet-5", In: 100, Out: 10, TS: 1000}
	must(t, s.UpsertCall(c))
	c.Out = 50
	must(t, s.UpsertCall(c))
	c.Out = 30 // record tardío con menos: no pisa el máximo
	must(t, s.UpsertCall(c))

	var n, out int64
	must(t, s.QueryRow(`SELECT COUNT(*), MAX(output_tokens) FROM calls`).Scan(&n, &out))
	if n != 1 || out != 50 {
		t.Fatalf("calls=%d out=%d, quiero 1/50", n, out)
	}
}

// La ley 1 sobre Postgres: el costo es una VISTA, priced es ENTERO (para que
// SUM(priced) del Go funcione), sin precio → NULL.
func TestPostgresCostoEsVista(t *testing.T) {
	s := openPG(t)
	seed(t, s)
	must(t, s.UpsertCall(Call{MessageID: "m1", SessionID: "sess-1", AgentID: "main",
		Model: "glm-4.7", In: 1_000_000, TS: 1000}))

	var priced int
	var cost *float64
	must(t, s.QueryRow(`SELECT priced, cost_usd FROM call_costs`).Scan(&priced, &cost))
	if priced != 0 || cost != nil {
		t.Fatalf("sin precio: priced=%d cost=%v", priced, cost)
	}
	_, err := s.Exec(`INSERT INTO prices (model, speed, input, output, source)
		VALUES ('glm-4.7','standard',?,?,'user')`, 0.6, 2.2)
	must(t, err)
	must(t, s.QueryRow(`SELECT priced, cost_usd FROM call_costs`).Scan(&priced, &cost))
	if priced != 1 || cost == nil || *cost < 0.59 || *cost > 0.61 {
		t.Fatalf("con precio: priced=%d cost=%v, quiero ~0.60", priced, cost)
	}
	// el agregado que usa el Go real (prices.go): SUM(priced), SUM(1-priced).
	var sumPriced, sumUnpriced int64
	must(t, s.QueryRow(`SELECT COALESCE(SUM(priced),0), COALESCE(SUM(1-priced),0) FROM call_costs`).
		Scan(&sumPriced, &sumUnpriced))
	if sumPriced != 1 || sumUnpriced != 0 {
		t.Fatalf("SUM priced/unpriced = %d/%d, quiero 1/0", sumPriced, sumUnpriced)
	}
}

// La columna "offset" es palabra RESERVADA en Postgres. Round-trip: si el
// quoting está bien, guarda y lee; si no, la query ni parsea.
func TestPostgresOffsetReservado(t *testing.T) {
	s := openPG(t)
	must(t, s.SetOffset("/a/b.jsonl", Offset{Dev: 1, Ino: 2, Offset: 4096}, 1000))
	o, err := s.GetOffset("/a/b.jsonl")
	must(t, err)
	if o.Dev != 1 || o.Ino != 2 || o.Offset != 4096 {
		t.Fatalf("offset round-trip = %+v, quiero dev1 ino2 off4096", o)
	}
	// idempotente: re-set avanza el offset.
	must(t, s.SetOffset("/a/b.jsonl", Offset{Dev: 1, Ino: 2, Offset: 8192}, 2000))
	o, _ = s.GetOffset("/a/b.jsonl")
	if o.Offset != 8192 {
		t.Fatalf("offset = %d, quiero 8192", o.Offset)
	}
}

// SeedBuiltinPrices y Costs() sobre Postgres: es el camino EXACTO del arranque
// (_ = st.SeedBuiltinPrices()) y de /api/stats. Atrapa la clase de bug de saltarse
// el rebind (s.DB.Exec con '?' crudos) que dejaría todos los costos en NULL.
func TestPostgresSeedBuiltinPricesYCosts(t *testing.T) {
	s := openPG(t)
	must(t, s.SeedBuiltinPrices())
	var n int64
	must(t, s.QueryRow(`SELECT COUNT(*) FROM prices WHERE source='builtin'`).Scan(&n))
	if n < 5 {
		t.Fatalf("builtin prices sembrados = %d, quiero >= 5 (¿se saltó el rebind?)", n)
	}
	// una llamada de un modelo builtin → Costs() la cotiza (no NULL).
	seed(t, s)
	must(t, s.UpsertCall(Call{MessageID: "c", SessionID: "sess-1", AgentID: "main",
		Model: "claude-opus-4-8", In: 1_000_000, TS: 1000})) // 1M in @ $5/M = $5
	c, err := s.Costs()
	must(t, err)
	if c.PricedCalls != 1 || c.UnpricedCalls != 0 || c.CostUSD < 4.99 || c.CostUSD > 5.01 {
		t.Fatalf("Costs = %+v, quiero 1 priced / $5", c)
	}
}

// BIGINT no desborda: un timestamp en ms (>2³¹) que en INTEGER de Postgres
// reventaría, aquí entra y sale intacto.
func TestPostgresBigIntNoDesborda(t *testing.T) {
	s := openPG(t)
	seed(t, s)
	big := int64(1_760_000_000_000) // ms, ~2025 — no cabe en int32
	must(t, s.UpsertCall(Call{MessageID: "big", SessionID: "sess-1", AgentID: "main",
		Model: "x", In: 3_000_000_000, TS: big})) // >2³¹ tokens también
	var ts, in int64
	must(t, s.QueryRow(`SELECT ts, input_tokens FROM calls WHERE message_id='big'`).Scan(&ts, &in))
	if ts != big || in != 3_000_000_000 {
		t.Fatalf("ts=%d in=%d — BIGINT desbordó", ts, in)
	}
}
