package initflow

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
)

// ── los pasos LLM: claude -p headless, con contrato JSON y degradación ──
// El LLM propone, el humano dispone: nada de lo que salga de aquí se aplica
// sin pasar por el borrador editable. Sin claude o con salida rota, el wizard
// sigue con los defaults deterministas (degrada, no explota).
//
// TRANSPARENCIA (la queja real del primer usuario: "parece que no hace
// nada"): el runner corre con --output-format stream-json y narra en la
// bitácora QUÉ está pasando — el comando exacto, el modelo, cada herramienta
// que usa, el texto que va escribiendo, y un latido con el tiempo cada 15s.

type llmRunner struct {
	Bin     string
	Timeout time.Duration
	Dir     string
	Purpose string // procedencia: "arqueología · svc-x" — sin esto la sesión
	// aparece huérfana y "(sin texto)" en el panel (la queja real)
}

func newLLM(dir string) *llmRunner {
	bin := os.Getenv("HARNESS_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	return &llmRunner{Bin: bin, Timeout: 5 * time.Minute, Dir: dir}
}

// newSessionID — uuid v4 (mismo formato que el plano de operar).
func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RecordRun — la procedencia de un run headless en <ws>/.harness/runs.jsonl
// (el mismo registro que usan las tareas del panel): quién lanzó la sesión y
// para qué. kind "one-shot" = corrió y terminó; el panel la etiqueta
// «Terminada» en vez de «En reposo».
func RecordRun(ws, purpose, session, kind string) {
	if ws == "" || session == "" {
		return
	}
	_ = os.MkdirAll(filepath.Join(ws, ".harness"), 0o755)
	f, err := os.OpenFile(filepath.Join(ws, ".harness", "runs.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"ts": time.Now().Unix(), "task": purpose,
		"session": session, "kind": kind})
	_, _ = f.Write(append(b, '\n'))
}

func clip(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// runJSON corre el prompt y parsea el JSON del contrato. 1 retry. Tolerante
// al shape del CLI: stream-json si lo hay; envelope {"result": …} o texto
// crudo como fallback (los stubs de test y CLIs viejos).
func (r *llmRunner) runJSON(prompt string, out any, log func(string)) error {
	path, err := exec.LookPath(r.Bin)
	if err != nil {
		return fmt.Errorf("no encuentro el CLI '%s' en PATH", r.Bin)
	}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt > 1 {
			log("segundo intento — la salida anterior no parseó")
		}
		text, err := r.stream(path, prompt, log)
		if err != nil {
			lastErr = err
			continue
		}
		blob := extractJSONBlock(text)
		if blob == "" {
			lastErr = errors.New("la salida del LLM no trae JSON")
			continue
		}
		if err := json.Unmarshal([]byte(blob), out); err != nil {
			lastErr = fmt.Errorf("el JSON del LLM no encaja en el contrato: %v", err)
			continue
		}
		return nil
	}
	return lastErr
}

// stream ejecuta claude narrando el progreso a la bitácora.
func (r *llmRunner) stream(path, prompt string, log func(string)) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()
	args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}
	// session-id conocido + registro de procedencia: la sesión aparece en el
	// panel con su propósito («arqueología · svc-x»), jamás huérfana
	if sid := newSessionID(); sid != "" && r.Dir != "" {
		args = append(args, "--session-id", sid)
		RecordRun(r.Dir, defaultStr2(r.Purpose, "init · paso LLM"), sid, "one-shot")
	}
	log(fmt.Sprintf("$ %s -p «prompt %dKB» --output-format stream-json · timeout %s · cwd %s",
		filepath.Base(path), (len(prompt)+1023)/1024, r.Timeout, defaultStr2(r.Dir, "(daemon)")))
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = r.Dir
	cmd.Stdin = nil
	var errb bytes.Buffer
	cmd.Stderr = &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return "", err
	}
	// latido: aunque el modelo esté callado, el humano ve que hay pulso
	hbDone := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbDone:
				return
			case <-t.C:
				log(fmt.Sprintf("⏳ el modelo sigue trabajando · %ds", int(time.Since(start).Seconds())))
			}
		}
	}()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024) // el result final puede ser enorme
	var result string
	var raw strings.Builder
	tools := 0
	for sc.Scan() {
		line := sc.Text()
		raw.WriteString(line)
		raw.WriteByte('\n')
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev["type"] {
		case "system":
			if ev["subtype"] == "init" {
				if mdl, _ := ev["model"].(string); mdl != "" {
					log("modelo: " + mdl)
				}
			}
		case "assistant":
			if msg, ok := ev["message"].(map[string]any); ok {
				if cont, ok := msg["content"].([]any); ok {
					for _, c := range cont {
						cm, _ := c.(map[string]any)
						switch cm["type"] {
						case "tool_use":
							tools++
							name, _ := cm["name"].(string)
							log(fmt.Sprintf("→ herramienta %s (llamada #%d)", name, tools))
						case "text":
							if t, _ := cm["text"].(string); strings.TrimSpace(t) != "" {
								log("✍ " + clip(t, 110))
							}
						}
					}
				}
			}
		case "result":
			if s, _ := ev["result"].(string); s != "" {
				result = s
			}
			if c, ok := ev["total_cost_usd"].(float64); ok {
				log(fmt.Sprintf("✓ el modelo terminó · %ds · $%.3f", int(time.Since(start).Seconds()), c))
			}
		}
	}
	werr := cmd.Wait()
	close(hbDone)
	if werr != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("timeout de %s — el modelo no terminó a tiempo", r.Timeout)
		}
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("claude -p falló: %s", clip(msg, 200))
		}
		return "", fmt.Errorf("claude -p falló: %v", werr)
	}
	if result == "" {
		// CLIs viejos / stubs: envelope {"result": …} o texto crudo
		result = extractResult([]byte(raw.String()))
	}
	return result, nil
}

func defaultStr2(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

// extractResult: el envelope de `--output-format json` trae .result (string).
// Si el shape cambió entre versiones del CLI, caemos al texto crudo.
func extractResult(raw []byte) string {
	var env struct {
		Result string `json:"result"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Result != "" {
		return env.Result
	}
	// puede venir como JSONL con un envelope por línea
	for _, line := range strings.Split(string(raw), "\n") {
		if json.Unmarshal([]byte(line), &env) == nil && env.Result != "" {
			return env.Result
		}
	}
	return string(raw)
}

// extractJSONBlock: primer bloque {…} balanceado (fuera de strings).
func extractJSONBlock(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// ── paso enrich: la entrevista 2a en modo propuesta ──

type enrichOut struct {
	Clusters   []gen.Cluster `json:"clusters"`
	DAG        []string      `json:"dag"`
	Principles []string      `json:"principles"`
	Recommendations map[string]struct {
		Value    any    `json:"value"`
		Evidence string `json:"evidence"`
	} `json:"recommendations"`
}

func (m *Manager) runEnrich() error {
	m.mu.Lock()
	ws := m.st.Workspace
	inv := m.inv
	seed := m.st.Answers
	overrides := m.st.RoleOverrides
	m.mu.Unlock()
	if inv == nil || seed == nil {
		return errors.New("corre el discover primero")
	}
	invJSON, _ := json.Marshal(inv)
	seedJSON, _ := json.Marshal(map[string]any{"clusters": seed.Clusters, "dag": seed.DAG})
	promptTmpl, err := gen.Asset("prompts/discovery-enrichment.md")
	if err != nil {
		return fmt.Errorf("prompt embebido: %w", err)
	}
	prompt := strings.NewReplacer(
		"{{INVENTORY_JSON}}", string(invJSON),
		"{{SEED_JSON}}", string(seedJSON),
	).Replace(string(promptTmpl))

	nServices := 0
	for _, r := range inv.Repos {
		if inv.RoleOf(r.Name, overrides) == "service" {
			nServices++
		}
	}
	m.logs.Append("enrich", fmt.Sprintf("consultando al modelo: %d repos (%d services) → clustering, DAG, principios…", inv.RepoCount, nServices))
	// el enrichment solo necesita el inventory (va inline en el prompt): en
	// remoto corre LOCAL con tu claude — la ruta del VPS no existe aquí
	dir := ws
	if m.isRemote() {
		dir = ""
	}
	runner := newLLM(dir)
	runner.Purpose = "init · enriquecimiento (clustering/DAG)"
	var out enrichOut
	if err := runner.runJSON(prompt, &out, func(s string) { m.logs.Append("enrich", s) }); err != nil {
		return fmt.Errorf("%v — puedes saltar este paso: los defaults deterministas ya están puestos", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Recommendations == nil {
		m.st.Recommendations = map[string]string{}
	}
	// la propuesta REFINA el borrador (el humano la edita después en la UI)
	if len(out.Clusters) > 0 {
		valid := true
		for _, c := range out.Clusters {
			if c.Agent == "" || !gen.ValidClusterKind(c.Kind) {
				valid = false
			}
		}
		if valid {
			// COBERTURA POR CÓDIGO, no por prompt: todo service sin abogado
			// se agrega aquí — el modelo propone nombres/dominios, la regla
			// la impone el sistema determinista (la casa siempre gana).
			cs, added := gen.EnsureServiceCoverage(out.Clusters, m.inv, m.st.RoleOverrides)
			m.st.Answers.Clusters = cs
			for _, a := range added {
				m.logs.Append("enrich", "⚠︎ el modelo omitió el servicio «"+a+"» — abogado svc-"+a+" agregado por la regla de cobertura")
			}
			m.st.Recommendations["clusters"] = "propuesto por el modelo leyendo el inventory"
			m.logs.Append("enrich", fmt.Sprintf("clusters aplicados: %d (%d agregados por cobertura)", len(cs), len(added)))
		} else {
			m.logs.Append("enrich", "clusters del modelo inválidos — me quedo con los deterministas")
		}
	}
	if len(out.DAG) > 0 {
		m.st.Answers.DAG = out.DAG
		m.st.Recommendations["dag"] = "orden propuesto por el modelo"
	}
	if len(out.Principles) > 0 && len(m.st.Answers.Principles) == 0 {
		m.st.Answers.Principles = out.Principles
		m.st.Recommendations["principles"] = "sugerencias del modelo — edítalas o bórralas"
	}
	for field, rec := range out.Recommendations {
		if rec.Evidence != "" {
			m.st.Recommendations[field] = rec.Evidence
		}
	}
	m.st.AnswersRev++
	m.persistLocked()
	m.logs.Append("enrich", fmt.Sprintf("✓ propuesta en el borrador: %d clusters · DAG de %d pasos — revisa y edita en Agentes", len(m.st.Answers.Clusters), len(m.st.Answers.DAG)))
	return nil
}
