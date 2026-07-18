package initflow

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallForPorOS(t *testing.T) {
	spec := "brew install jq | apt-get install -y jq"
	if cmd, auto, sudo := installFor(spec, "darwin"); cmd != "brew install jq" || !auto || sudo {
		t.Fatalf("darwin: %q %v %v", cmd, auto, sudo)
	}
	if cmd, auto, sudo := installFor(spec, "linux"); cmd != "sudo apt-get install -y jq" || auto || !sudo {
		t.Fatalf("linux: %q %v %v", cmd, auto, sudo)
	}
	// sin alternativa por OS → instrucción manual (la primera)
	if cmd, auto, _ := installFor("npm install -g x", "darwin"); cmd != "npm install -g x" || auto {
		t.Fatalf("manual: %q %v", cmd, auto)
	}
}

func TestRequirementsConPathControlado(t *testing.T) {
	setupEnv(t)
	bins := t.TempDir()
	for _, b := range []string{"git", "jq", "claude"} {
		script := fmt.Sprintf("#!/bin/sh\necho %s version 1.0\n", b)
		if err := os.WriteFile(filepath.Join(bins, b), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bins) // gh y herdr NO están — son opcionales
	m := New("t", (&adoptSpy{}).fn)
	res, code := m.Handle("requirements-check", map[string]any{})
	if code != 200 {
		t.Fatalf("check: %d", code)
	}
	reqs := res.(map[string]any)["requirements"].([]ReqState)
	byName := map[string]ReqState{}
	for _, r := range reqs {
		byName[r.Name] = r
	}
	if !byName["git"].OK || !byName["jq"].OK || !byName["claude"].OK {
		t.Fatalf("los stubs deben detectarse: %+v", byName)
	}
	if byName["gh"].OK || !byName["gh"].Optional {
		t.Fatalf("gh ausente y opcional: %+v", byName["gh"])
	}
	if byName["git"].Version == "" {
		t.Fatal("la versión debe capturarse")
	}

	// el runner pasa: todo lo requerido está
	home, _ := os.UserHomeDir()
	if _, code := m.Handle("workspace", map[string]any{"path": filepath.Join(home, "ws"), "create": true}); code != 200 {
		t.Fatal("workspace")
	}
	if _, code := m.Handle("step", map[string]any{"step": "requirements", "action": "run"}); code != 200 {
		t.Fatal("run")
	}
	waitStep(t, m, "requirements", OK, 3*time.Second)

	// sin claude en PATH → el runner falla nombrándolo
	if err := os.Remove(filepath.Join(bins, "claude")); err != nil {
		t.Fatal(err)
	}
	if _, code := m.Handle("step", map[string]any{"step": "requirements", "action": "retry"}); code != 200 {
		t.Fatal("retry")
	}
	waitStep(t, m, "requirements", Fail, 3*time.Second)
	if st := findStep(m, "requirements"); st.Error == "" || !contains(st.Error, "claude") {
		t.Fatalf("el error debe nombrar lo que falta: %q", st.Error)
	}
}

func TestInstallDesconocidoYManual(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	if _, code := m.Handle("install", map[string]any{"name": "yolo"}); code != 400 {
		t.Fatal("dependencia fuera de baseline → 400")
	}
	// claude: install es npm → manual, jamás auto-run
	res, code := m.Handle("install", map[string]any{"name": "claude"})
	if code != 200 {
		t.Fatalf("install claude: %d", code)
	}
	r := res.(map[string]any)
	if r["ok"] != false || r["manual"] != true {
		t.Fatalf("npm es manual: %v", r)
	}
}

func findStep(m *Manager, id string) StepState {
	for _, s := range m.Public().Steps {
		if s.ID == id {
			return s
		}
	}
	return StepState{}
}

func waitStep(t *testing.T, m *Manager, id string, want Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s := findStep(m, id)
		if s.Status == want {
			return
		}
		if s.Status == Fail && want != Fail {
			t.Fatalf("paso %s falló: %s", id, s.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout esperando %s=%s (está %s)", id, want, s.Status)
		}
		time.Sleep(30 * time.Millisecond)
	}
}
