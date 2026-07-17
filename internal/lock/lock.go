// Package lock implementa el singleton del daemon.
//
// EL PUERTO ES EL LOCK. No hay pidfile de exclusión, no hay lockdir, no hay
// ventana de adquisición. Ver docs/adr/ADR-0005-singleton-por-puerto.md
//
// Contexto de por qué esto importa tanto: el ship.sh del harness usaba mkdir
// como lock y escribía el pid DESPUÉS. Un SIGKILL en esa ventana dejaba un
// lockdir sin pid que el reclamo de huérfanos no podía reclamar: el lock quedaba
// inmortal y todo ship futuro de ese repo moría a los 10 minutos, para siempre.
// Lo reprodujimos y lo arreglamos (harness-creator v0.11.1).
//
// El kernel libera el puerto al morir el proceso, atómicamente, siempre. No hay
// ventana que cerrar. `kill -9` no deja basura. Es un mutex que no se puede
// corromper — y nos costó un bug aprenderlo.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Protocol es la versión del contrato HTTP. Sube solo si /health o el ingest
// cambian de forma incompatible; el JSONL del cable no cuenta, porque los hooks
// son tan tontos que ninguna versión los puede romper (a propósito).
const Protocol = 1

// Health es lo que devuelve /health. Sirve para distinguir "nuestro daemon" de
// "algo más escuchando en 7718" — que en una laptop de dev pasa.
type Health struct {
	Name     string `json:"name"`     // siempre "harnessd"
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	PID      int    `json:"pid"`
	DB       string `json:"db"`
	Mode     string `json:"mode"` // all-in-one | collect | serve
	Started  int64  `json:"started"`
}

// ErrAlreadyRunning: otro daemon nuestro ya tiene el puerto. No es un error de
// verdad — es el resultado esperado en nueve de cada diez `ensure`.
var ErrAlreadyRunning = errors.New("ya hay un harnessd corriendo")

// ErrPortBusyForeign: el puerto está ocupado por algo que no somos nosotros.
// Esto SÍ es un problema y hay que decírselo al humano, no fingir que ganamos.
var ErrPortBusyForeign = errors.New("el puerto está ocupado por otro proceso que no es harnessd")

// Acquire intenta ser el daemon. Devuelve el listener si ganamos.
//
// Siempre 127.0.0.1, NUNCA 0.0.0.0: este panel enseña tu código y tus tareas.
// Exponerlo a la LAN por accidente no es una opción que queramos ofrecer.
func Acquire(port int) (net.Listener, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	// Alguien tiene el puerto. ¿Somos nosotros?
	if h, herr := Probe(port); herr == nil && h.Name == "harnessd" {
		return nil, fmt.Errorf("%w (pid %d, v%s)", ErrAlreadyRunning, h.PID, h.Version)
	}
	return nil, ErrPortBusyForeign
}

// Probe pregunta a quien tenga el puerto quién es.
func Probe(port int) (Health, error) {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return Health{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Health{}, fmt.Errorf("health devolvió %d", resp.StatusCode)
	}
	var h Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return Health{}, err
	}
	return h, nil
}

// WaitHealthy espera a que el daemon responda. Lo usa el supervisor (bash) tras
// un update: si no responde, revierte a .prev. Ver ADR-0006.
func WaitHealthy(port int, d time.Duration) (Health, error) {
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		h, err := Probe(port)
		if err == nil {
			return h, nil
		}
		last = err
		time.Sleep(150 * time.Millisecond)
	}
	return Health{}, fmt.Errorf("no respondió en %s: %w", d, last)
}
