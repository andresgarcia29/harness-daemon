package initflow

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
)

// ── paso archaeology: subagentes LLM llenan abogados y specs (DRAFT) ──
// Máx 4 en paralelo. Falla parcial NO bloquea: los esqueletos ya existen y
// el estado reporta qué cluster quedó sin arqueología. Skippeable entero.

type ArchState struct {
	Agent  string `json:"agent"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func (m *Manager) runArchaeology() error {
	m.mu.Lock()
	ws := m.st.Workspace
	a := m.st.Answers
	m.mu.Unlock()
	if a == nil {
		return errors.New("no hay configuración confirmada")
	}
	var services []gen.Cluster
	for _, c := range a.Clusters {
		if c.Kind == "service" {
			services = append(services, c)
		}
	}
	if len(services) == 0 {
		m.logs.Append("archaeology", "sin clusters de servicio — nada que excavar")
		return nil
	}
	promptTmpl, err := gen.Asset("prompts/archaeology-service.md")
	if err != nil {
		return fmt.Errorf("prompt embebido: %w", err)
	}
	// estado inicial visible
	m.mu.Lock()
	m.st.Archaeology = nil
	for _, c := range services {
		m.st.Archaeology = append(m.st.Archaeology, ArchState{Agent: c.Agent, Status: Pending})
	}
	m.persistLocked()
	m.mu.Unlock()

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	fails := 0
	for i, c := range services {
		wg.Add(1)
		go func(i int, c gen.Cluster) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m.setArch(i, Running, "")
			dom := strings.TrimPrefix(c.Agent, "svc-")
			prompt := strings.NewReplacer(
				"{{AGENT_NAME}}", c.Agent,
				"{{REPOS_CSV}}", strings.Join(c.Repos, ", "),
				"{{PREFIX}}", strings.ToUpper(dom),
			).Replace(string(promptTmpl))
			var f gen.ArchFinding
			runner := newLLM(ws)
			runner.Timeout = 8 * time.Minute
			if err := runner.runJSON(prompt, &f, func(s string) { m.logs.Append("archaeology", c.Agent+": "+s) }); err != nil {
				mu.Lock()
				fails++
				mu.Unlock()
				m.setArch(i, Fail, err.Error())
				m.logs.Append("archaeology", "❌ "+c.Agent+": "+err.Error())
				return
			}
			if err := gen.Restamp(ws, a, c, &f, gen.Opts{WS: ws, Version: m.version, Now: time.Now()}); err != nil {
				mu.Lock()
				fails++
				mu.Unlock()
				m.setArch(i, Fail, err.Error())
				m.logs.Append("archaeology", "⚠︎ "+c.Agent+": "+err.Error())
				return
			}
			m.setArch(i, OK, f.Owns)
			m.logs.Append("archaeology", "✓ "+c.Agent+" — "+f.Owns)
		}(i, c)
	}
	wg.Wait()
	if fails > 0 {
		return fmt.Errorf("%d cluster(s) sin arqueología — reintenta o salta (los esqueletos DRAFT ya existen)", fails)
	}
	return nil
}

func (m *Manager) setArch(i int, s Status, detail string) {
	m.mu.Lock()
	if i >= 0 && i < len(m.st.Archaeology) {
		m.st.Archaeology[i].Status = s
		m.st.Archaeology[i].Detail = detail
	}
	m.persistLocked()
	m.mu.Unlock()
}
