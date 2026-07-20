package api

import (
	"testing"

	"github.com/andresgarcia29/harness-daemon/internal/herdr"
)

func liveByOf(snap *Snapshot, id string) string {
	for _, s := range snap.Sessions {
		if s.ID == id {
			return s.LiveBy
		}
	}
	return "<no-existe>"
}

func TestEnrichLiveness(t *testing.T) {
	base := func() *Snapshot {
		return &Snapshot{Sessions: []Session{
			{ID: "trabaja", Cwd: "/ws/worktrees/COR-1", NActive: 2},
			{ID: "abierta-idle", Cwd: "/ws/worktrees/COR-2", NActive: 1},
			{ID: "sin-pane-reciente", Cwd: "/ws/worktrees/COR-3", NActive: 1},
			{ID: "reposo", Cwd: "/ws/worktrees/COR-4", NActive: 0},
		}}
	}

	// herdr disponible: sólo "herdr" cuando hay un pane suyo TRABAJANDO.
	h := herdr.State{Available: true, Panes: []herdr.Pane{
		{PaneID: "p1", ForegroundCwd: "/ws/worktrees/COR-1", AgentStatus: "working"},
		{PaneID: "p2", ForegroundCwd: "/ws/worktrees/COR-2", AgentStatus: "idle"}, // abierta, no trabaja
	}}
	snap := base()
	EnrichLiveness(snap, h)
	if got := liveByOf(snap, "trabaja"); got != "herdr" {
		t.Errorf("sesión con pane working: live_by=%q, quería herdr", got)
	}
	if got := liveByOf(snap, "abierta-idle"); got != "" {
		t.Errorf("terminal abierta pero agente idle: live_by=%q, quería vacío (no miente 'trabajando')", got)
	}
	if got := liveByOf(snap, "sin-pane-reciente"); got != "recent" {
		t.Errorf("sin pane pero mtime reciente: live_by=%q, quería recent", got)
	}
	if got := liveByOf(snap, "reposo"); got != "" {
		t.Errorf("en reposo: live_by=%q, quería vacío", got)
	}

	// Prefijo: pane con foreground_cwd en subcarpeta de la sesión también cuenta.
	h2 := herdr.State{Available: true, Panes: []herdr.Pane{
		{PaneID: "p", Cwd: "/ws/worktrees/COR-1/src", AgentStatus: "blocked"},
	}}
	snap2 := base()
	EnrichLiveness(snap2, h2)
	if got := liveByOf(snap2, "trabaja"); got != "herdr" {
		t.Errorf("pane en subcarpeta + blocked cuenta como vivo: live_by=%q", got)
	}

	// cwd COMPARTIDO (dos sesiones en la raíz del workspace): con UN pane
	// trabajando ahí, sólo la que escribe de verdad (NActive>0) se enciende; la
	// hermana muerta (NActive=0) NO — así no se encienden 8 a la vez.
	shared := &Snapshot{Sessions: []Session{
		{ID: "viva-raiz", Cwd: "/ws", NActive: 1},
		{ID: "muerta-raiz", Cwd: "/ws", NActive: 0},
	}}
	hShared := herdr.State{Available: true, Panes: []herdr.Pane{
		{PaneID: "p", ForegroundCwd: "/ws", AgentStatus: "working"},
	}}
	EnrichLiveness(shared, hShared)
	if got := liveByOf(shared, "viva-raiz"); got != "herdr" {
		t.Errorf("cwd compartido, sesión activa: live_by=%q, quería herdr", got)
	}
	if got := liveByOf(shared, "muerta-raiz"); got != "" {
		t.Errorf("cwd compartido, sesión muerta: live_by=%q, quería vacío (no encender hermanas)", got)
	}

	// herdr NO disponible: cae al mtime (no regresa a quien no usa herdr).
	snap3 := base()
	EnrichLiveness(snap3, herdr.State{Available: false})
	if got := liveByOf(snap3, "trabaja"); got != "live" {
		t.Errorf("sin herdr, NActive>0 → live (mtime): live_by=%q", got)
	}
	if got := liveByOf(snap3, "reposo"); got != "" {
		t.Errorf("sin herdr, NActive=0 → vacío: live_by=%q", got)
	}
}
