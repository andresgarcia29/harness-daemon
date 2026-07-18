package initflow

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// arma un workspace con repos git falsos y el paso workspace ya hecho.
func wsConRepos(t *testing.T, m *Manager) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	ws := filepath.Join(home, "ws")
	if _, code := m.Handle("workspace", map[string]any{"path": ws, "create": true}); code != 200 {
		t.Fatal("workspace")
	}
	// atlas: service Go (go.mod + Dockerfile + cmd/)
	atlas := filepath.Join(ws, "repos", "atlas")
	mustGit(t, "", "init", "-q", atlas)
	os.MkdirAll(filepath.Join(atlas, "cmd"), 0o755)
	os.WriteFile(filepath.Join(atlas, "go.mod"), []byte("module atlas\n"), 0o644)
	os.WriteFile(filepath.Join(atlas, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	// tf-core: infra-module (variables.tf + outputs.tf)
	tf := filepath.Join(ws, "repos", "tf-core")
	mustGit(t, "", "init", "-q", tf)
	os.WriteFile(filepath.Join(tf, "main.tf"), []byte("resource {}\n"), 0o644)
	os.WriteFile(filepath.Join(tf, "variables.tf"), []byte("\n"), 0o644)
	os.WriteFile(filepath.Join(tf, "outputs.tf"), []byte("\n"), 0o644)
	return ws
}

func runStepOK(t *testing.T, m *Manager, id string) {
	t.Helper()
	if _, code := m.Handle("step", map[string]any{"step": id, "action": "run"}); code != 200 {
		t.Fatalf("run %s", id)
	}
	waitStep(t, m, id, OK, 15*time.Second)
}

func TestDiscoverSiembraElBorrador(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	ws := wsConRepos(t, m)
	runStepOK(t, m, "discover")

	if _, err := os.Stat(filepath.Join(ws, "inventory.json")); err != nil {
		t.Fatal("inventory.json es el artefacto del paso")
	}
	p := m.Public()
	if p.Inventory == nil || p.Inventory.RepoCount != 2 {
		t.Fatalf("inventario: %+v", p.Inventory)
	}
	if p.Answers == nil || p.AnswersRev != 1 {
		t.Fatalf("el borrador debe sembrarse con rev 1: rev=%d", p.AnswersRev)
	}
	var kinds []string
	for _, c := range p.Answers.Clusters {
		kinds = append(kinds, c.Agent+":"+c.Kind)
	}
	if len(p.Answers.Clusters) != 2 || kinds[0] != "svc-atlas:service" || kinds[1] != "infra:infra" {
		t.Fatalf("clustering determinista: %v", kinds)
	}
	if len(p.Answers.DAG) != 2 || p.Answers.DAG[0] != "tf-core" || p.Answers.DAG[1] != "atlas" {
		t.Fatalf("DAG por capas: %v", p.Answers.DAG)
	}
	// recomendaciones de capacidades por señal (go-toolchain por go.mod)
	if _, ok := p.Recommendations["capability:go-toolchain"]; !ok {
		t.Fatalf("go-toolchain debió recomendarse: %v", p.Recommendations)
	}
}

func TestAnswersPatchConRev(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	wsConRepos(t, m)
	runStepOK(t, m, "discover")

	// rev equivocada → 409 con la rev vigente
	if r, code := m.Handle("answers", map[string]any{"rev": 99, "patch": map[string]any{"flow": "prs"}}); code != 409 {
		t.Fatalf("rev mala: %d %v", code, r)
	}
	// patch válido → rev sube
	if r, code := m.Handle("answers", map[string]any{"rev": 1, "patch": map[string]any{
		"flow": "trunk-staging", "project": map[string]any{"ticket_prefix": "COR"},
	}}); code != 200 {
		t.Fatalf("patch: %d %v", code, r)
	}
	p := m.Public()
	if p.Answers.Flow != "trunk-staging" || p.Answers.Project.TicketPrefix != "COR" || p.AnswersRev != 2 {
		t.Fatalf("merge: flow=%s prefix=%s rev=%d", p.Answers.Flow, p.Answers.Project.TicketPrefix, p.AnswersRev)
	}
	if p.Answers.Project.Name == "" {
		t.Fatal("el merge parcial no debe borrar lo no tocado")
	}
	// enum inválido → 400 y nada cambia
	if _, code := m.Handle("answers", map[string]any{"rev": 2, "patch": map[string]any{"flow": "yolo"}}); code != 400 {
		t.Fatal("enum inválido")
	}
	if m.Public().Answers.Flow != "trunk-staging" {
		t.Fatal("un patch rechazado no muta el borrador")
	}
}

func TestRoleOverrideNoDestructivo(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	wsConRepos(t, m)
	runStepOK(t, m, "discover")
	// corregir un rol JAMÁS borra clusters (pisaba el enrich y tus ediciones):
	// atlas→library deja svc-atlas; tú lo quitas en Agentes si quieres
	if _, code := m.Handle("role", map[string]any{"repo": "atlas", "role": "library"}); code != 200 {
		t.Fatal("override")
	}
	found := false
	for _, c := range m.Public().Answers.Clusters {
		if c.Agent == "svc-atlas" {
			found = true
		}
	}
	if !found {
		t.Fatal("el override no debe borrar clusters existentes")
	}
	// un repo SIN cluster que se vuelve service SÍ gana abogado (cobertura):
	// primero saca tf-core del cluster infra vía patch…
	rev := m.Public().AnswersRev
	if _, code := m.Handle("answers", map[string]any{"rev": rev, "patch": map[string]any{
		"clusters": []any{map[string]any{"agent": "svc-atlas", "kind": "service", "repos": []any{"atlas"}}},
	}}); code != 200 {
		t.Fatal("patch clusters")
	}
	// …y al reclasificarlo, la cobertura le da su abogado
	if _, code := m.Handle("role", map[string]any{"repo": "tf-core", "role": "service"}); code != 200 {
		t.Fatal("override a service")
	}
	found = false
	for _, c := range m.Public().Answers.Clusters {
		if c.Agent == "svc-tf-core" && c.Owns != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("volverse service agrega abogado: %+v", m.Public().Answers.Clusters)
	}
	if _, code := m.Handle("role", map[string]any{"repo": "atlas", "role": "yolo"}); code != 400 {
		t.Fatal("rol inválido")
	}
}

func TestEnrichConStubYDegradación(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	wsConRepos(t, m)
	runStepOK(t, m, "discover")

	// stub bueno: envelope de --output-format json con result JSON
	stubDir := t.TempDir()
	good := filepath.Join(stubDir, "claude")
	payload := `{\"clusters\":[{\"agent\":\"svc-identidad\",\"kind\":\"service\",\"repos\":[\"atlas\"],\"owns\":\"identidad\"}],\"dag\":[\"tf-core\",\"atlas\"],\"principles\":[\"contratos primero\"]}`
	script := fmt.Sprintf("#!/bin/sh\necho '{\"result\": \"%s\"}'\n", payload)
	if err := os.WriteFile(good, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HARNESS_CLAUDE_BIN", good)
	runStepOK(t, m, "enrich")
	p := m.Public()
	if len(p.Answers.Clusters) != 1 || p.Answers.Clusters[0].Agent != "svc-identidad" || p.Answers.Clusters[0].Owns != "identidad" {
		t.Fatalf("la propuesta del modelo debe refinar el borrador: %+v", p.Answers.Clusters)
	}
	if len(p.Answers.Principles) != 1 {
		t.Fatalf("principles: %v", p.Answers.Principles)
	}

	// stub roto → el paso falla con mensaje de degradación (y se puede saltar)
	bad := filepath.Join(stubDir, "claude-bad")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\necho 'no soy json'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HARNESS_CLAUDE_BIN", bad)
	if _, code := m.Handle("step", map[string]any{"step": "enrich", "action": "retry"}); code != 200 {
		t.Fatal("retry")
	}
	waitStep(t, m, "enrich", Fail, 10*time.Second)
	if st := findStep(m, "enrich"); !contains(st.Error, "saltar") {
		t.Fatalf("el error debe ofrecer el skip: %q", st.Error)
	}
}
