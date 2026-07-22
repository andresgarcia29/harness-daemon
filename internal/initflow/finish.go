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

// CountTasks — el verify por artefacto, standalone para el init-step remoto.
func CountTasks(ws string) int {
	entries, err := os.ReadDir(filepath.Join(ws, "tasks"))
	if err != nil {
		return 0
	}
	return len(entries)
}

func (m *Manager) runFirstTask() error {
	m.mu.Lock()
	ws := m.st.Workspace
	m.mu.Unlock()
	n := 0
	if m.isRemote() {
		var err error
		if n, err = m.remoteFirstTask(ws); err != nil {
			return err
		}
	} else {
		n = CountTasks(ws)
	}
	if n == 0 {
		return errors.New("aún no hay tareas — crea una desde el formulario (o salta este paso)")
	}
	m.logs.Append("first-task", fmt.Sprintf("✓ %d tarea(s) en tasks/", n))
	return nil
}

// ── paso finish: el doctor embebido como verificación final determinista.
// Verde (0 fallos) → el plano de init se apaga (ADR-0011: al terminar, las
// leyes de siempre vuelven a regir completas).

// RunDoctor extrae y corre el doctor embebido sobre ws. Standalone: lo usan
// el Manager local y `harness init-step finish` (remoto).
func RunDoctor(ws string, log func(string)) error {
	script, err := gen.Asset("scripts/doctor.sh")
	if err != nil {
		return err
	}
	path := filepath.Join(ws, ".harness", "init", "doctor.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
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
	var fails []string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			log(line)
			last = line
			// el error del finish es un PROMPT: sin la lista de ❌, quien lo
			// lee (humano o agente) tiene que ir a pescar al log del doctor
			if strings.Contains(line, "❌") {
				fails = append(fails, line)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if len(fails) > 0 {
			last = last + " · " + strings.Join(fails, " · ")
		}
		return fmt.Errorf("el doctor reporta fallos (%s) — si son binarios faltantes, `make init` en el workspace instala las capacidades elegidas; corrige y reintenta", last)
	}
	return nil
}

func (m *Manager) runFinish() error {
	m.mu.Lock()
	ws := m.st.Workspace
	m.mu.Unlock()
	if m.isRemote() {
		if err := m.remoteFinish(ws); err != nil {
			return err
		}
	} else {
		if err := RunDoctor(ws, func(s string) { m.logs.Append("finish", s) }); err != nil {
			return err
		}
	}
	m.logs.Append("finish", "✓ doctor en verde — el harness está listo")
	m.mu.Lock()
	m.st.CompletedAt = time.Now().Unix()
	m.st.Active = false // el plano de init se apaga (mutaciones → 410)
	m.persistLocked()
	m.mu.Unlock()
	return nil
}
