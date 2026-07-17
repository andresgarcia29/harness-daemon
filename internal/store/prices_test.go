package store

import "testing"

func TestSeedCotizaYEsIdempotente(t *testing.T) {
	s := open(t)
	seed(t, s)
	must(t, s.SeedBuiltinPrices())
	must(t, s.SeedBuiltinPrices()) // dos arranques: cero duplicados (PK lo garantiza)

	must(t, s.UpsertCall(Call{MessageID: "m1", SessionID: "sess-1", AgentID: "main",
		Model: "claude-sonnet-5", In: 1_000_000, Out: 1_000_000, TS: 1000}))
	c, err := s.Costs()
	must(t, err)
	if c.PricedCalls != 1 || c.UnpricedCalls != 0 {
		t.Fatalf("priced/unpriced = %d/%d", c.PricedCalls, c.UnpricedCalls)
	}
	if c.CostUSD < 17.9 || c.CostUSD > 18.1 { // 3 + 15
		t.Fatalf("cost = %v, quiero ~18.0", c.CostUSD)
	}
}

// La edición del usuario GANA para siempre: re-seedear no la pisa.
func TestSeedNoPisaAlUsuario(t *testing.T) {
	s := open(t)
	_, err := s.DB.Exec(`INSERT INTO prices (model, speed, valid_from, input, output, source)
		VALUES ('claude-sonnet-5','standard',0,99,99,'user')`)
	must(t, err)
	must(t, s.SeedBuiltinPrices())
	var input float64
	var source string
	must(t, s.DB.QueryRow(`SELECT input, source FROM prices
		WHERE model='claude-sonnet-5' AND valid_from=0`).Scan(&input, &source))
	if input != 99 || source != "user" {
		t.Fatalf("el seed pisó al usuario: input=%v source=%s", input, source)
	}
}

// Lo no cotizable se DICE: el total viaja con cuántas llamadas quedaron fuera.
func TestCostosDicenLoQueNoSaben(t *testing.T) {
	s := open(t)
	seed(t, s)
	must(t, s.SeedBuiltinPrices())
	must(t, s.UpsertCall(Call{MessageID: "a", SessionID: "sess-1", AgentID: "main",
		Model: "claude-sonnet-5", In: 1_000_000, TS: 1}))
	must(t, s.UpsertCall(Call{MessageID: "b", SessionID: "sess-1", AgentID: "main",
		Model: "glm-sin-precio", In: 1_000_000, TS: 1}))
	c, err := s.Costs()
	must(t, err)
	if c.UnpricedCalls != 1 {
		t.Fatalf("unpriced = %d, quiero 1 — el total sin esto parece completo y no lo es", c.UnpricedCalls)
	}
}
