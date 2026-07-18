// session.go — el drill-down de razonamiento por agente (/api/session), armado
// del store: agentes con su usage/costo + el hilo (texto/pensamiento/tool) que
// el colector persiste ya redactado. Es la última pieza de paridad de lectura.
package api

import (
	"database/sql"

	"github.com/andresgarcia29/harness-daemon/internal/store"
)

type threadItem struct {
	K   string `json:"k"` // text | think | tool
	TS  int64  `json:"ts"`
	T   string `json:"t"`
	Inp string `json:"inp,omitempty"`
}
type agentDetail struct {
	ID      string       `json:"id"`
	Who     string       `json:"who"`
	Type    string       `json:"type"`
	Model   string       `json:"model"`
	Active  bool         `json:"active"`
	Depth   int          `json:"depth"`
	FirstTS int64        `json:"first_ts"`
	LastTS  int64        `json:"last_ts"`
	Elapsed int64        `json:"elapsed"`
	Usage   usage        `json:"usage"`
	Cost    *float64     `json:"cost"`
	Thread  []threadItem `json:"thread"`
}
type SessionDetail struct {
	ID     string        `json:"id"`
	Short  string        `json:"short"`
	Agents []agentDetail `json:"agents"`
}

// BuildSession arma el detalle de UNA sesión desde el store.
func BuildSession(db store.Queryer, sessionID string, now int64) (*SessionDetail, error) {
	prices, _ := loadPrices(db)
	// usage + primer/último ts por agente (de sus llamadas — el reloj bueno)
	au := map[string]*usage{}
	amodel := map[string]string{}
	amodelTS := map[string]int64{}
	aFirst := map[string]int64{}
	aLast := map[string]int64{}
	rows, err := db.Query(`SELECT agent_id, model, input_tokens, output_tokens,
		cache_read_tokens, cache_write_5m_tokens, cache_write_1h_tokens, ts
		FROM calls WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var aid, model string
		var in, out, cr, cw5, cw1, ts int64
		if rows.Scan(&aid, &model, &in, &out, &cr, &cw5, &cw1, &ts) != nil {
			continue
		}
		if au[aid] == nil {
			au[aid] = &usage{}
		}
		u := au[aid]
		u.In += in
		u.Out += out
		u.CRead += cr
		u.CCrea += cw5 + cw1
		if ts >= amodelTS[aid] {
			amodelTS[aid] = ts
			amodel[aid] = model
		}
		if ts > 0 {
			if aFirst[aid] == 0 || ts < aFirst[aid] {
				aFirst[aid] = ts
			}
			if ts > aLast[aid] {
				aLast[aid] = ts
			}
		}
	}
	rows.Close()

	d := &SessionDetail{ID: sessionID, Short: short(sessionID), Agents: []agentDetail{}}
	ar, err := db.Query(`SELECT id, type, description, spawn_depth, started, last_seen
		FROM agents WHERE session_id = ? ORDER BY (id != 'main'), started`, sessionID)
	if err != nil {
		return nil, err
	}
	defer ar.Close()
	for ar.Next() {
		var aid, typ, desc string
		var depth int
		var started, last int64
		if ar.Scan(&aid, &typ, &desc, &depth, &started, &last) != nil {
			continue
		}
		u := usage{}
		if au[aid] != nil {
			u = *au[aid]
		}
		model := amodel[aid]
		p, ok := prices[model]
		first, lst := aFirst[aid], aLast[aid]
		if first == 0 {
			first = started
		}
		if lst == 0 {
			lst = last
		}
		el := int64(0)
		if first > 0 && lst > 0 {
			el = lst - first
		}
		who := desc
		if aid == "main" {
			who = "orquestador"
		} else if who == "" {
			who = typ
			if who == "" && len(aid) > 10 {
				who = aid[:10]
			} else if who == "" {
				who = aid
			}
		}
		thr := []threadItem{}
		items, _ := db.Query(`SELECT ts, kind, text, COALESCE(hint,'')
			FROM (SELECT seq, ts, kind, text, hint FROM agent_thread
			      WHERE session_id = ? AND agent_id = ? ORDER BY seq DESC LIMIT 80)
			ORDER BY seq ASC`, sessionID, aid)
		if items != nil {
			for items.Next() {
				var it threadItem
				var ts sql.NullInt64
				if items.Scan(&ts, &it.K, &it.T, &it.Inp) == nil {
					if ts.Valid {
						it.TS = ts.Int64
					}
					thr = append(thr, it)
				}
			}
			items.Close()
		}
		d.Agents = append(d.Agents, agentDetail{
			ID: aid, Who: who, Type: typ, Model: model,
			Active: lst > 0 && now-lst < activeWindow, Depth: depth,
			FirstTS: first, LastTS: lst, Elapsed: el, Usage: u,
			Cost: costOf(p, u, ok), Thread: thr,
		})
	}
	return d, nil
}
