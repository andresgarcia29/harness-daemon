package initflow

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
)

// ── paso first-task: la primera tarea se crea por el plano de operar normal
// (/api/op/task — el workspace ya está adoptado). El runner solo VERIFICA el
// artefacto: existe al menos una tarea. Saltable.

func (m *Manager) runFirstTask() error {
	m.mu.Lock()
	ws := m.st.Workspace
	m.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(ws, "tasks"))
	if err != nil || len(entries) == 0 {
		return errors.New("aún no hay tareas — crea una desde el formulario (o salta este paso)")
	}
	m.logs.Append("first-task", fmt.Sprintf("✓ %d tarea(s) en tasks/", len(entries)))
	return nil
}

// ── paso finish: el doctor embebido como verificación final determinista.
// Verde (0 fallos) → el plano de init se apaga (ADR-0011: al terminar, las
// leyes de siempre vuelven a regir completas).

func (m *Manager) runFinish() error {
	m.mu.Lock()
	ws := m.st.Workspace
	m.mu.Unlock()
	script, err := gen.Asset("scripts/doctor.sh")
	if err != nil {
		return err
	}
	path := filepath.Join(ws, ".harness", "init", "doctor.sh")
	if err := os.WriteFile(path, script, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("bash", path, ws)
	cmd.Dir = ws
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	last := ""
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			m.logs.Append("finish", line)
			last = line
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("el doctor reporta fallos (%s) — corrígelos y reintenta, o revisa el log", last)
	}
	m.logs.Append("finish", "✓ doctor en verde — el harness está listo")
	m.mu.Lock()
	m.st.CompletedAt = time.Now().Unix()
	m.st.Active = false // el plano de init se apaga (mutaciones → 410)
	m.persistLocked()
	m.mu.Unlock()
	return nil
}
