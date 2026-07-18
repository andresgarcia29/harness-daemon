package initflow

import (
	"os"
	"path/filepath"
	"testing"
)

// adoptSpy registra las adopciones y puede fallar a propósito.
type adoptSpy struct {
	calls []string
	fail  error
}

func (a *adoptSpy) fn(path string) error {
	a.calls = append(a.calls, path)
	return a.fail
}

func setupEnv(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_CONFIG_DIR", filepath.Join(home, ".config", "harness"))
	return home
}

func TestNewFresco(t *testing.T) {
	setupEnv(t)
	m := New("0.1.0-test", (&adoptSpy{}).fn)
	p := m.Public()
	if !p.Active || p.Step != "workspace" {
		t.Fatalf("estado inicial raro: %+v", p)
	}
	if len(p.Steps) != len(order) {
		t.Fatalf("pasos: %d", len(p.Steps))
	}
	if _, err := os.Stat(cfgStatePath()); err != nil {
		t.Fatalf("el estado debe persistirse al nacer: %v", err)
	}
}

func TestWorkspaceValidación(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	for _, bad := range []string{"", "../fuera", "/x/../y", "relativa/ruta", "/tmp/fuera-de-home"} {
		res, code := m.Handle("workspace", map[string]any{"path": bad})
		if code != 400 {
			t.Fatalf("path %q debió dar 400, dio %d: %v", bad, code, res)
		}
	}
	// fuera de home CON confirmación → pasa la validación (dry_run)
	_, code := m.Handle("workspace", map[string]any{"path": "/tmp/fuera-de-home", "confirm_outside_home": true, "dry_run": true})
	if code != 200 {
		t.Fatalf("confirm_outside_home debió permitir el dry_run, dio %d", code)
	}
}

func TestWorkspaceDryRunNoAdopta(t *testing.T) {
	home := setupEnv(t)
	spy := &adoptSpy{}
	m := New("t", spy.fn)
	res, code := m.Handle("workspace", map[string]any{"path": filepath.Join(home, "ws"), "dry_run": true})
	if code != 200 {
		t.Fatalf("dry_run: %d %v", code, res)
	}
	r := res.(map[string]any)
	if r["exists"] != false || r["writable"] != true {
		t.Fatalf("probe de carpeta inexistente bajo home: %v", r)
	}
	if len(spy.calls) != 0 {
		t.Fatal("dry_run jamás adopta")
	}
	if m.WorkspacePath() != "" {
		t.Fatal("dry_run no debe fijar el workspace")
	}
}

func TestWorkspaceCreaYAdopta(t *testing.T) {
	home := setupEnv(t)
	spy := &adoptSpy{}
	m := New("t", spy.fn)
	ws := filepath.Join(home, "mi-ws")

	// sin create → 400 con instrucción
	if _, code := m.Handle("workspace", map[string]any{"path": ws}); code != 400 {
		t.Fatalf("sin create debió dar 400, dio %d", code)
	}
	res, code := m.Handle("workspace", map[string]any{"path": ws, "create": true})
	if code != 200 {
		t.Fatalf("crear: %d %v", code, res)
	}
	if len(spy.calls) != 1 || spy.calls[0] != ws {
		t.Fatalf("adopt: %v", spy.calls)
	}
	if fi, err := os.Stat(filepath.Join(ws, "repos")); err != nil || !fi.IsDir() {
		t.Fatal("debe crear <ws>/repos")
	}
	if _, err := os.Stat(wsStatePath(ws)); err != nil {
		t.Fatal("el estado canónico debe vivir en el workspace")
	}
	p := m.Public()
	if p.Step != "github" || p.Steps[0].Status != OK {
		t.Fatalf("tras fijar workspace: %+v", p)
	}

	// idempotente: el mismo path otra vez → ok, sin re-adoptar
	if _, code := m.Handle("workspace", map[string]any{"path": ws}); code != 200 {
		t.Fatal("re-mandar el mismo workspace es idempotente")
	}
	if len(spy.calls) != 1 {
		t.Fatal("no debe re-adoptar")
	}
	// otro path → 409
	if _, code := m.Handle("workspace", map[string]any{"path": filepath.Join(home, "otro"), "create": true}); code != 409 {
		t.Fatal("cambiar de workspace a mitad de init es 409")
	}
}

func TestResumeTrasReinicio(t *testing.T) {
	home := setupEnv(t)
	spy := &adoptSpy{}
	m := New("t", spy.fn)
	ws := filepath.Join(home, "ws")
	if _, code := m.Handle("workspace", map[string]any{"path": ws, "create": true}); code != 200 {
		t.Fatal("no pude fijar el workspace")
	}
	// "reinicio": un manager nuevo con el mismo ConfigDir re-adopta solo
	spy2 := &adoptSpy{}
	m2 := New("t", spy2.fn)
	if got := m2.WorkspacePath(); got != ws {
		t.Fatalf("resume: workspace %q", got)
	}
	if len(spy2.calls) != 1 || spy2.calls[0] != ws {
		t.Fatalf("resume debe re-adoptar: %v", spy2.calls)
	}
	if p := m2.Public(); p.Step != "github" {
		t.Fatalf("resume debe retomar en github: %s", p.Step)
	}
}

func TestStepSkipYNoImplementado(t *testing.T) {
	home := setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	if _, code := m.Handle("workspace", map[string]any{"path": filepath.Join(home, "ws"), "create": true}); code != 200 {
		t.Fatal("workspace")
	}
	// skip de un paso no saltable → 400
	if _, code := m.Handle("step", map[string]any{"step": "clone", "action": "skip"}); code != 400 {
		t.Fatal("clone no es saltable")
	}
	// skip de enrich (saltable) → ok
	res, code := m.Handle("step", map[string]any{"step": "enrich", "action": "skip"})
	if code != 200 {
		t.Fatalf("skip enrich: %d %v", code, res)
	}
	// run de un paso sin runner en esta versión → 400 honesto
	if _, code := m.Handle("step", map[string]any{"step": "github", "action": "run"}); code != 400 {
		t.Fatal("paso sin implementar debe declarar que no existe aún")
	}
	// paso desconocido → 400
	if _, code := m.Handle("step", map[string]any{"step": "yolo", "action": "run"}); code != 400 {
		t.Fatal("paso desconocido")
	}
}

func TestAttach(t *testing.T) {
	home := setupEnv(t)
	spy := &adoptSpy{}
	m := New("t", spy.fn)
	ws := filepath.Join(home, "ws")
	if _, code := m.Handle("workspace", map[string]any{"path": ws, "create": true}); code != 200 {
		t.Fatal("workspace")
	}
	if m2 := Attach("t", ws, spy.fn); m2 == nil || m2.Public().Step != "github" {
		t.Fatal("Attach debe cargar el init a medias del workspace")
	}
	if m3 := Attach("t", t.TempDir(), spy.fn); m3 != nil {
		t.Fatal("Attach sin init activo debe dar nil")
	}
}

func TestLogBufferReplay(t *testing.T) {
	l := NewLogBuffer()
	l.Append("clone", "línea 1")
	l.Append("clone", "línea 2")
	replay, ch, cancel := l.Subscribe(0)
	defer cancel()
	if len(replay) != 2 {
		t.Fatalf("replay: %d", len(replay))
	}
	l.Append("clone", "línea 3")
	select {
	case ll := <-ch:
		if ll.Line != "línea 3" || ll.Seq != 3 {
			t.Fatalf("vivo: %+v", ll)
		}
	default:
		t.Fatal("el suscriptor debió recibir la línea viva")
	}
	if tail := l.Tail("clone", 2); len(tail) != 2 || tail[1] != "línea 3" {
		t.Fatalf("tail: %v", tail)
	}
	// replay parcial desde Last-Event-ID
	replay2, _, cancel2 := l.Subscribe(2)
	defer cancel2()
	if len(replay2) != 1 || replay2[0].Line != "línea 3" {
		t.Fatalf("replay parcial: %+v", replay2)
	}
}
