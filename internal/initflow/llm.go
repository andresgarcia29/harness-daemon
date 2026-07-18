package initflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
)

// ── los pasos LLM: claude -p headless, con contrato JSON y degradación ──
// El LLM propone, el humano dispone: nada de lo que salga de aquí se aplica
// sin pasar por el borrador editable. Sin claude o con salida rota, el wizard
// sigue con los defaults deterministas (degrada, no explota).

type llmRunner struct {
	Bin     string
	Timeout time.Duration
	Dir     string
}

func newLLM(dir string) *llmRunner {
	bin := os.Getenv("HARNESS_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	return &llmRunner{Bin: bin, Timeout: 5 * time.Minute, Dir: dir}
}

// runJSON corre `claude -p <prompt> --output-format json`, extrae el campo
// .result y parsea el JSON que el prompt exigió. 1 retry. Tolerante al shape:
// si .result no existe usa el stdout crudo; si el texto trae fences o prosa
// alrededor, extrae el primer bloque {...} balanceado.
func (r *llmRunner) runJSON(prompt string, out any, log func(string)) error {
	path, err := exec.LookPath(r.Bin)
	if err != nil {
		return fmt.Errorf("no encuentro el CLI '%s' en PATH", r.Bin)
	}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt > 1 {
			log("reintento del paso LLM (la salida no parseó)")
		}
		ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
		cmd := exec.CommandContext(ctx, path, "-p", prompt, "--output-format", "json")
		cmd.Dir = r.Dir
		cmd.Stdin = nil
		raw, err := cmd.Output()
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("claude -p falló: %v", err)
			continue
		}
		text := extractResult(raw)
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

// extractResult: el envelope de `--output-format json` trae .result (string).
// Si el shape cambió entre versiones del CLI, caemos al stdout crudo.
func extractResult(raw []byte) string {
	var env struct {
		Result string `json:"result"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Result != "" {
		return env.Result
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
	Clusters []gen.Cluster `json:"clusters"`
	DAG      []string      `json:"dag"`
	Principles []string    `json:"principles"`
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

	m.logs.Append("enrich", "consultando al modelo (clustering, DAG, principios)…")
	// el enrichment solo necesita el inventory (va inline en el prompt): en
	// remoto corre LOCAL con tu claude — la ruta del VPS no existe aquí
	dir := ws
	if m.isRemote() {
		dir = ""
	}
	var out enrichOut
	if err := newLLM(dir).runJSON(prompt, &out, func(s string) { m.logs.Append("enrich", s) }); err != nil {
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
			m.st.Answers.Clusters = out.Clusters
			m.st.Recommendations["clusters"] = "propuesto por el modelo leyendo el inventory"
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
	m.logs.Append("enrich", fmt.Sprintf("✓ propuesta aplicada al borrador (%d clusters, %d pasos de DAG)", len(m.st.Answers.Clusters), len(m.st.Answers.DAG)))
	return nil
}
