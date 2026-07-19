package initflow

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestE2EResumeCompleto — el criterio del plan: la secuencia entera con
// stubs, RECREANDO el manager entre cada paso (como si el daemon muriera).
// Cada paso re-verifica por artefacto; nada se re-hace de más.
func TestE2EResumeCompleto(t *testing.T) {
	home := setupEnv(t)
	// PATH: claude stubbeado; git/jq/bash reales (doctor los necesita de verdad).
	// Los bins de las capacidades CLI sembradas también se stubbean: en el
	// mundo real los instala `make init`; aquí el doctor solo debe verlos.
	stubs := t.TempDir()
	payload := `{\"owns\":\"todo el dominio\",\"not_owns\":\"nada\",\"invariants\":[\"i1\"],\"requirements\":[]}`
	if err := os.WriteFile(filepath.Join(stubs, "claude"),
		[]byte(fmt.Sprintf("#!/bin/sh\necho '{\"result\": \"%s\"}'\n", payload)), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"go", "gitleaks", "semgrep", "ccusage", "go-arch-lint", "gh"} {
		if err := os.WriteFile(filepath.Join(stubs, b),
			[]byte("#!/bin/sh\necho "+b+" 1.0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", stubs+":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin")
	t.Setenv("HARNESS_CLAUDE_BIN", filepath.Join(stubs, "claude"))
	apiStub(t, "pat456")

	spy := &adoptSpy{}
	reborn := func() *Manager { return New("9.9.9-e2e", spy.fn) } // "reinicio del daemon"
	m := reborn()
	ws := filepath.Join(home, "ws")

	// 1. workspace
	if _, code := m.Handle("workspace", map[string]any{"path": ws, "create": true}); code != 200 {
		t.Fatal("workspace")
	}
	// repos git falsos (el clone se simula pre-clonando: verify por artefacto)
	atlas := filepath.Join(ws, "repos", "atlas")
	mustGit(t, "", "init", "-q", atlas)
	os.MkdirAll(filepath.Join(atlas, "cmd"), 0o755)
	os.WriteFile(filepath.Join(atlas, "go.mod"), []byte("module atlas\n"), 0o644)
	os.WriteFile(filepath.Join(atlas, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	mustGit(t, atlas, "add", ".")
	mustGit(t, atlas, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	mustGit(t, atlas, "remote", "add", "origin", "https://github.com/corvux/atlas.git")

	// 2. github (PAT contra el stub REST)
	m = reborn()
	if m.Public().Step != "github" {
		t.Fatalf("resume en github, está en %s", m.Public().Step)
	}
	if _, code := m.Handle("github", map[string]any{"mode": "pat", "token": "pat456"}); code != 200 {
		t.Fatal("github")
	}
	// 3. selección + clone (ya clonado → verify lo salta)
	m = reborn()
	if _, code := m.Handle("repos", map[string]any{"repos": []any{map[string]any{"full_name": "corvux/atlas"}}}); code != 200 {
		t.Fatal("repos")
	}
	runStepOK(t, m, "clone")
	// 4. requirements
	m = reborn()
	runStepOK(t, m, "requirements")
	// 5. discover + skip enrich
	m = reborn()
	runStepOK(t, m, "discover")
	if _, code := m.Handle("step", map[string]any{"step": "enrich", "action": "skip"}); code != 200 {
		t.Fatal("skip enrich")
	}
	// 6. generate
	m = reborn()
	if m.Public().Answers == nil {
		t.Fatal("el borrador debe sobrevivir el reinicio")
	}
	runStepOK(t, m, "generate")
	// 7. archaeology (stub)
	m = reborn()
	runStepOK(t, m, "archaeology")
	// 8. mcps (sin MCPs elegidos: materializa y sigue)
	m = reborn()
	runStepOK(t, m, "mcps")
	// 9. first-task: sin tareas falla; con task.md pasa
	m = reborn()
	if _, code := m.Handle("step", map[string]any{"step": "first-task", "action": "run"}); code != 200 {
		t.Fatal("run first-task")
	}
	waitStep(t, m, "first-task", Fail, 5*time.Second)
	os.MkdirAll(filepath.Join(ws, "tasks", "T-1"), 0o755)
	os.WriteFile(filepath.Join(ws, "tasks", "T-1", "task.md"), []byte("---\nid: T-1\n---\n"), 0o644)
	if _, code := m.Handle("step", map[string]any{"step": "first-task", "action": "retry"}); code != 200 {
		t.Fatal("retry first-task")
	}
	waitStep(t, m, "first-task", OK, 5*time.Second)
	// 10. finish: doctor sobre el ws generado
	m = reborn()
	runStepOK(t, m, "finish")
	p := m.Public()
	if p.Active || p.CompletedAt == 0 {
		t.Fatalf("finish debe apagar el plano: %+v", p)
	}
	// el plano apagado contesta 410
	if _, code := m.Handle("step", map[string]any{"step": "finish", "action": "run"}); code != 410 {
		t.Fatal("tras finish, las mutaciones son 410")
	}
	// artefactos finales
	if b, _ := os.ReadFile(filepath.Join(ws, ".harness-version")); string(b) != "9.9.9-e2e\n" {
		t.Fatalf(".harness-version: %q", b)
	}
	// un manager nuevo CONSERVA el init terminado (no lo pisa con uno fresco)
	m2 := New("9.9.9-e2e", spy.fn)
	if p := m2.Public(); p.Active || p.CompletedAt == 0 {
		t.Fatalf("el init terminado se conserva: %+v", p)
	}
	if _, code := m2.Handle("workspace", map[string]any{"path": ws}); code != 410 {
		t.Fatal("init terminado → 410 salvo restart")
	}
	// restart explícito → wizard fresco; el ws YA instalado se rechaza con guía
	if _, code := m2.Handle("restart", map[string]any{}); code != 200 {
		t.Fatal("restart")
	}
	if p := m2.Public(); !p.Active || p.Step != "workspace" {
		t.Fatalf("tras restart: %+v", p)
	}
	if _, code := m2.Handle("workspace", map[string]any{"path": ws}); code != 409 {
		t.Fatal("workspace ya inicializado → 409 con guía")
	}
}
