package api

import (
	"strings"

	"github.com/andresgarcia29/harness-daemon/internal/herdr"
)

// EnrichLiveness cambia la mentira del mtime por VERDAD DE CAMPO: cruza cada
// sesión (por su cwd) con las terminales vivas de herdr. Una sesión sólo se
// marca "trabajando" (live_by="herdr") si herdr confirma un pane suyo con un
// agente activo. Si herdr no está disponible, cae al mtime (comportamiento
// previo) para no regresar a quien no usa herdr.
//
// Por qué: el panel marcaba "activo" cualquier transcript tocado en los últimos
// 90s — sin chequear proceso vivo. Resultado: sesiones "trabajando" sin nada
// corriendo, y que "nunca se cierran". herdr sí sabe qué corre de verdad.
func EnrichLiveness(snap *Snapshot, h herdr.State) {
	if snap == nil {
		return
	}
	// Cuántas sesiones comparten cada cwd. Las de worktrees/<tarea> son únicas
	// (match preciso); las que corren en la raíz del workspace colisionan — ahí
	// desambiguamos con el mtime (sólo la que escribe de verdad se enciende).
	cwdCount := map[string]int{}
	for i := range snap.Sessions {
		if c := snap.Sessions[i].Cwd; c != "" {
			cwdCount[c]++
		}
	}
	for i := range snap.Sessions {
		s := &snap.Sessions[i]
		if !h.Available {
			if s.NActive > 0 {
				s.LiveBy = "live" // sin herdr, el mtime es la mejor señal que hay
			}
			continue
		}
		working, anyPane := false, false
		if s.Cwd != "" {
			for _, p := range h.Panes {
				pc := p.ForegroundCwd
				if pc == "" {
					pc = p.Cwd
				}
				if pc == "" || !cwdMatch(pc, s.Cwd) {
					continue
				}
				anyPane = true
				if p.AgentStatus == "working" || p.AgentStatus == "blocked" {
					working = true
				}
			}
		}
		// cwd único → confiamos en herdr aunque el mtime esté frío (una llamada
		// larga no factura hace rato pero el agente SÍ trabaja). cwd compartido →
		// exigimos actividad reciente para no encender sesiones muertas hermanas.
		unique := cwdCount[s.Cwd] <= 1
		switch {
		case working && (unique || s.NActive > 0):
			s.LiveBy = "herdr" // una terminal de herdr la corre AHORA
		case anyPane:
			s.LiveBy = "" // terminal abierta / trabajo de otra sesión en el mismo cwd
		case s.NActive > 0:
			s.LiveBy = "recent" // sin pane; el mtime dice reciente, sin confirmar
		default:
			s.LiveBy = ""
		}
	}
}

// cwdMatch: mismo directorio, o uno contiene al otro (worktrees anidados,
// pane con foreground_cwd en una subcarpeta de la sesión).
func cwdMatch(a, b string) bool {
	a = strings.TrimRight(a, "/")
	b = strings.TrimRight(b, "/")
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
