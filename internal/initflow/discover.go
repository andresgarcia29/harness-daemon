package initflow

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
)

// ── paso discover: el script determinista del instalador, embebido ──
// Extrae scripts/discover.sh (bash 3.2 + jq, cero tokens) al workspace y lo
// corre. Verify = <ws>/inventory.json parsea y cubre los repos clonados.

func (m *Manager) runDiscover() error {
	m.mu.Lock()
	ws := m.st.Workspace
	m.mu.Unlock()

	script, err := gen.Asset("scripts/discover.sh")
	if err != nil {
		return fmt.Errorf("discover.sh embebido: %w", err)
	}
	dir := filepath.Join(ws, ".harness", "init")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "discover.sh")
	if err := os.WriteFile(path, script, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("bash", path, ws)
	cmd.Dir = ws
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // mismo pipe: el resumen humano del script al log
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			m.logs.Append("discover", line)
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("discover.sh falló (%v) — ¿repos clonados en repos/? ¿jq instalado?", err)
	}
	inv, err := gen.LoadInventory(ws)
	if err != nil {
		return fmt.Errorf("inventory.json no parsea: %w", err)
	}
	m.mu.Lock()
	m.inv = inv
	m.mu.Unlock()
	m.logs.Append("discover", fmt.Sprintf("✓ %d repos inventariados · hints de secretos: %v", inv.RepoCount, inv.SecretHints))
	m.seedAnswersIfEmpty()
	return nil
}

// seedAnswersIfEmpty siembra el borrador determinista (una sola vez: si el
// humano ya editó, sus decisiones no se pisan).
func (m *Manager) seedAnswersIfEmpty() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inv == nil || m.st.Answers != nil {
		return
	}
	m.st.Answers = gen.SeedAnswers(m.inv, m.st.Workspace, m.st.RoleOverrides)
	m.st.AnswersRev = 1
	if m.st.Recommendations == nil {
		m.st.Recommendations = map[string]string{}
	}
	for name, ev := range gen.RecommendCapabilities(m.inv) {
		m.st.Recommendations["capability:"+name] = ev
	}
	m.persistLocked()
}

// handleRole — corrección humana del rol inferido de un repo (tab Hallazgos).
// Re-deriva clusters/DAG SOLO si el humano aún no los editó a mano.
func (m *Manager) handleRole(body map[string]any) (any, int) {
	repo, role := str(body, "repo"), str(body, "role")
	if !gen.ValidRole(role) {
		return map[string]any{"ok": false, "error": "rol inválido: " + role}, 400
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inv == nil {
		return map[string]any{"ok": false, "error": "corre el discover primero"}, 409
	}
	found := false
	for _, r := range m.inv.Repos {
		if r.Name == repo {
			found = true
		}
	}
	if !found {
		return map[string]any{"ok": false, "error": "repo desconocido: " + repo}, 400
	}
	if m.st.RoleOverrides == nil {
		m.st.RoleOverrides = map[string]string{}
	}
	m.st.RoleOverrides[repo] = role
	// re-siembra clusters/DAG desde las reglas (con overrides aplicados)
	if m.st.Answers != nil {
		m.st.Answers.Clusters = gen.SeedClusters(m.inv, m.st.RoleOverrides)
		m.st.Answers.DAG = gen.SeedDAG(m.inv, m.st.RoleOverrides)
		m.st.AnswersRev++
	}
	m.persistLocked()
	return map[string]any{"ok": true, "repo": repo, "role": role, "rev": m.st.AnswersRev}, 200
}

// handleAnswers — patch parcial del borrador con control de concurrencia por
// rev (el SSE jamás pisa un formulario: el cliente edita local y manda el
// patch con la rev que vio; rev distinta = 409 y la UI ofrece recargar).
func (m *Manager) handleAnswers(body map[string]any) (any, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Answers == nil {
		return map[string]any{"ok": false, "error": "aún no hay borrador — corre el discover"}, 409
	}
	if intv(body, "rev") != m.st.AnswersRev {
		return map[string]any{"ok": false, "error": "rev", "rev": m.st.AnswersRev,
			"detail": "otro cliente editó el borrador — recarga"}, 409
	}
	patch, _ := body["patch"].(map[string]any)
	if len(patch) == 0 {
		return map[string]any{"ok": false, "error": "patch vacío"}, 400
	}
	merged, err := gen.MergeAnswers(m.st.Answers, patch)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 400
	}
	if err := merged.Validate(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 400
	}
	m.st.Answers = merged
	m.st.AnswersRev++
	m.persistLocked()
	return map[string]any{"ok": true, "rev": m.st.AnswersRev}, 200
}

// handleAnswersConfirm — marca la entrevista como completa (paso enrich ya
// corrido o saltado; el paso "discover" agrupa ambos en la UI). Exige campos
// obligatorios llenos.
func (m *Manager) handleAnswersConfirm(map[string]any) (any, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.st.Answers
	if a == nil {
		return map[string]any{"ok": false, "error": "no hay borrador"}, 409
	}
	if err := a.Validate(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 400
	}
	if a.Project.Name == "" || a.Flow == "" || a.Autonomy == "" || a.Secrets.Source == "" {
		return map[string]any{"ok": false, "error": "faltan campos obligatorios (nombre, flow, autonomía, secretos)"}, 400
	}
	m.logs.Append("discover", "configuración confirmada por el usuario")
	m.persistLocked()
	return map[string]any{"ok": true}, 200
}
