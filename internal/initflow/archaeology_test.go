package initflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchaeologyRestamp(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	ws := wsConRepos(t, m)
	runStepOK(t, m, "discover")
	// generar primero (la arqueología re-estampa lo generado)
	runStepOK(t, m, "generate")

	payload := `{\"owns\":\"identidad y tenancy\",\"not_owns\":\"pagos\",\"invariants\":[\"tenant_id en toda tabla — evidencia: repos/atlas/go.mod\"],\"requirements\":[{\"id\":\"ATLAS-1\",\"title\":\"aislar tenants\",\"ears\":\"WHEN una query corre THE SYSTEM SHALL filtrar por tenant\",\"scenario\":\"Given...\",\"evidence\":\"repos/atlas/go.mod\"}]}`
	stub := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(stub, []byte(fmt.Sprintf("#!/bin/sh\necho '{\"result\": \"%s\"}'\n", payload)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HARNESS_CLAUDE_BIN", stub)
	runStepOK(t, m, "archaeology")

	ab, err := os.ReadFile(filepath.Join(ws, ".claude", "agents", "svc-atlas.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ab), "identidad y tenancy") || !strings.Contains(string(ab), "pagos") {
		t.Fatalf("el abogado debe llevar la arqueología: %s", ab)
	}
	sp, err := os.ReadFile(filepath.Join(ws, "specs", "atlas", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sp), "ATLAS-1") || !strings.Contains(string(sp), "DRAFT") {
		t.Fatalf("la spec debe llevar los requirements en DRAFT: %s", sp)
	}
	if st := m.Public(); len(st.Archaeology) != 1 || st.Archaeology[0].Status != OK {
		t.Fatalf("estado: %+v", st.Archaeology)
	}

	// personalización humana → la arqueología NO pisa
	agentPath := filepath.Join(ws, ".claude", "agents", "svc-atlas.md")
	if err := os.WriteFile(agentPath, []byte("MI ABOGADO EDITADO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code := m.Handle("step", map[string]any{"step": "archaeology", "action": "retry"}); code != 200 {
		t.Fatal("retry")
	}
	waitStep(t, m, "archaeology", Fail, 10*time.Second)
	if b, _ := os.ReadFile(agentPath); string(b) != "MI ABOGADO EDITADO\n" {
		t.Fatal("la personalización jamás se pisa")
	}
}
