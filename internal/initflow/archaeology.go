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
//
// El núcleo (RunArchaeology) es standalone: Manager local y `harness
// init-step archaeology` (remoto por ssh) corren el MISMO código.

type ArchState struct {
	Agent  string `json:"agent"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// RunArchaeology excava los clusters de servicio de a. progress se llama con
// cada transición (índice sobre la lista de servicios). only != "" excava
// SOLO ese cluster (el botón Re-excavar del panel). Devuelve el estado final
// por cluster y cuántos fallaron.
func RunArchaeology(ws string, a *gen.Answers, version string, log func(string), progress func(i int, st ArchState), only string) ([]ArchState, int) {
	var services []gen.Cluster
	for _, c := range a.Clusters {
		if c.Kind == "service" && (only == "" || c.Agent == only) {
			services = append(services, c)
		}
	}
	if only != "" && len(services) == 0 {
		log("❌ no hay cluster de servicio llamado «" + only + "»")
		return nil, 1
	}
	if len(services) == 0 {
		log("sin clusters de servicio — nada que excavar")
		return nil, 0
	}
	promptTmpl, err := gen.Asset("prompts/archaeology-service.md")
	if err != nil {
		log("❌ prompt embebido: " + err.Error())
		return nil, len(services)
	}
	out := make([]ArchState, len(services))
	for i, c := range services {
		out[i] = ArchState{Agent: c.Agent, Status: Pending}
		progress(i, out[i])
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	fails := 0
	set := func(i int, s Status, detail string) {
		mu.Lock()
		out[i].Status, out[i].Detail = s, detail
		st := out[i]
		mu.Unlock()
		progress(i, st)
	}
	for i, c := range services {
		wg.Add(1)
		go func(i int, c gen.Cluster) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			set(i, Running, "")
			dom := strings.TrimPrefix(c.Agent, "svc-")
			prompt := strings.NewReplacer(
				"{{AGENT_NAME}}", c.Agent,
				"{{REPOS_CSV}}", strings.Join(c.Repos, ", "),
				"{{PREFIX}}", strings.ToUpper(dom),
			).Replace(string(promptTmpl))
			var f gen.ArchFinding
			runner := newLLM(ws)
			runner.Timeout = 8 * time.Minute
			if err := runner.runJSON(prompt, &f, func(s string) { log(c.Agent + ": " + s) }); err != nil {
				mu.Lock()
				fails++
				mu.Unlock()
				set(i, Fail, err.Error())
				log("❌ " + c.Agent + ": " + err.Error())
				return
			}
			if err := gen.Restamp(ws, a, c, &f, gen.Opts{WS: ws, Version: version, Now: time.Now()}); err != nil {
				mu.Lock()
				fails++
				mu.Unlock()
				set(i, Fail, err.Error())
				log("⚠︎ " + c.Agent + ": " + err.Error())
				return
			}
			set(i, OK, f.Owns)
			log("✓ " + c.Agent + " — " + f.Owns)
		}(i, c)
	}
	wg.Wait()
	return out, fails
}

func (m *Manager) runArchaeology() error {
	m.mu.Lock()
	ws := m.st.Workspace
	a := m.st.Answers
	m.mu.Unlock()
	if a == nil {
		return errors.New("no hay configuración confirmada")
	}
	if m.isRemote() {
		return m.remoteArchaeology(ws)
	}
	// estado inicial visible antes de arrancar
	m.mu.Lock()
	m.st.Archaeology = nil
	m.persistLocked()
	m.mu.Unlock()
	_, fails := RunArchaeology(ws, a, m.version,
		func(s string) { m.logs.Append("archaeology", s) },
		func(i int, st ArchState) { m.setArch(i, st) }, "")
	if fails > 0 {
		return fmt.Errorf("%d cluster(s) sin arqueología — reintenta o salta (los esqueletos DRAFT ya existen)", fails)
	}
	return nil
}

func (m *Manager) setArch(i int, st ArchState) {
	m.mu.Lock()
	for len(m.st.Archaeology) <= i {
		m.st.Archaeology = append(m.st.Archaeology, ArchState{})
	}
	m.st.Archaeology[i] = st
	m.persistLocked()
	m.mu.Unlock()
}
