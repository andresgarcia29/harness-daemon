package initflow

import (
	"errors"
	"fmt"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
)

// ── paso generate: el generador determinista sobre el borrador confirmado ──

func (m *Manager) runGenerate() error {
	m.mu.Lock()
	ws := m.st.Workspace
	a := m.st.Answers
	inv := m.inv
	m.mu.Unlock()
	if a == nil {
		return errors.New("no hay configuración — completa la entrevista del paso anterior")
	}
	if inv == nil {
		return errors.New("no hay inventario — corre el auto-discover")
	}
	if err := a.Validate(); err != nil {
		return err
	}
	m.logs.Append("generate", "generando el harness (determinista, cero tokens)…")
	rep, err := gen.Generate(a, inv, gen.Opts{WS: ws, Version: m.version, Now: time.Now()})
	if err != nil {
		return err
	}
	for _, f := range rep.Files {
		switch f.Action {
		case "created":
			m.logs.Append("generate", "＋ "+f.Path)
		case "updated":
			m.logs.Append("generate", "↻ "+f.Path)
		case "conflict":
			m.logs.Append("generate", "⚠︎ "+f.Path+" — personalizado; propuesta en .new")
		}
	}
	m.logs.Append("generate", fmt.Sprintf("✓ %d creados · %d actualizados · %d intactos · %d conflictos",
		rep.Created, rep.Updated, rep.Kept, rep.Conflicts))
	if rep.Conflicts > 0 {
		m.logs.Append("generate", "revisa los .new — tu versión local se conservó")
	}
	return nil
}
