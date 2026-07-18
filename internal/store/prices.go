package store

// Precios builtin — el mismo estimado que templates/ui/pricing.json del
// instalador (USD por millón; multiplicadores de caché: lectura 0.1×,
// escritura 5m 1.25×, escritura 1h 2×). La báscula OFICIAL sigue siendo
// ccusage/la factura: esto existe para que la tendencia se vea sin salir.
//
// Ley (ADR-0004): un modelo que NO esté aquí cuesta NULL y la UI enseña "—".
// El usuario agrega o corrige con INSERT source='user' y valid_from=now — la
// vista call_costs toma el precio vigente a la fecha de CADA llamada, así que
// el histórico se re-cotiza solo y los precios viejos siguen cotizando lo viejo.
var builtinPrices = []struct {
	Model   string
	In, Out float64
}{
	{"claude-fable-5", 10, 50},
	{"claude-mythos-5", 10, 50},
	{"claude-opus-4-8", 5, 25},
	{"claude-opus-4-7", 5, 25},
	{"claude-opus-4-6", 5, 25},
	{"claude-opus-4-5", 5, 25},
	{"claude-sonnet-5", 3, 15},
	{"claude-sonnet-4-6", 3, 15},
	{"claude-sonnet-4-5", 3, 15},
	{"claude-haiku-4-5", 1, 5},
}

// SeedBuiltinPrices inserta los builtin en valid_from=0 SIN pisar nada:
// ON CONFLICT DO NOTHING — si el usuario editó una fila, su edición gana
// para siempre. Idempotente: se llama en cada arranque.
func (s *Store) SeedBuiltinPrices() error {
	for _, p := range builtinPrices {
		_, err := s.Exec(`
			INSERT INTO prices (model, speed, valid_from, provider, input, output,
			                    cache_read, cache_write_5m, cache_write_1h, source)
			VALUES (?,'standard',0,'anthropic',?,?,?,?,?,'builtin')
			ON CONFLICT(model, speed, valid_from) DO NOTHING`,
			p.Model, p.In, p.Out, p.In*0.1, p.In*1.25, p.In*2.0)
		if err != nil {
			return err
		}
	}
	return nil
}

// CostSummary — lo que /api/stats enseña de dinero. La distinción
// priced/unpriced viaja SIEMPRE junta: un total que calla cuántas llamadas
// no pudo cotizar es un número inventado con aspecto de dato.
type CostSummary struct {
	CostUSD       float64 `json:"cost_usd"` // solo lo cotizable
	PricedCalls   int64   `json:"priced_calls"`
	UnpricedCalls int64   `json:"unpriced_calls"` // > 0 → el total es un piso, no el total
}

func (s *Store) Costs() (CostSummary, error) {
	var c CostSummary
	err := s.QueryRow(`
		SELECT COALESCE(SUM(cost_usd), 0),
		       COALESCE(SUM(priced), 0),
		       COALESCE(SUM(1 - priced), 0)
		FROM call_costs`).Scan(&c.CostUSD, &c.PricedCalls, &c.UnpricedCalls)
	return c, err
}
