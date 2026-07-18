// Package initflow es el orquestador del wizard de onboarding (`harness init`).
//
// Leyes (ADR-0011):
//   - El estado del wizard es una máquina de pasos donde "hecho" se verifica
//     contra ARTEFACTOS (repos clonados, inventory parseable, .harness-version),
//     no contra flags. El state.json es cache/bitácora; la verdad está en disco.
//   - El navegador nunca manda comandos: solo selecciones sobre datos que el
//     servidor ya tiene (rutas validadas, nombres de catálogo, repos del listado
//     que el propio server obtuvo).
//   - Todo paso es idempotente y reanudable: re-entrar re-verifica.
package initflow

import "github.com/andresgarcia29/harness-daemon/internal/gen"

type Status string

const (
	Pending Status = "pending"
	Running Status = "running"
	OK      Status = "ok"
	Fail    Status = "fail"
	Skipped Status = "skipped"
)

// StepState es lo que el snapshot publica de cada paso.
type StepState struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   Status   `json:"status"`
	Detail   string   `json:"detail,omitempty"`
	Error    string   `json:"error,omitempty"`
	LogsTail []string `json:"logs_tail,omitempty"`
}

// State es el estado persistido del wizard. Los campos por-paso (repos,
// requirements, answers…) se agregan en sus fases; el esquema lleva Version
// para migrar hacia adelante.
type State struct {
	Version        int         `json:"version"`
	HarnessVersion string      `json:"harness_version"`
	Active         bool        `json:"active"`
	Workspace      string      `json:"workspace,omitempty"`
	Target         string      `json:"target,omitempty"` // "" = esta máquina; nombre de target = VPS (F11)
	Current        string      `json:"current"`
	Steps          []StepState `json:"steps"`
	GitHub         *GHState    `json:"github,omitempty"`
	Repos          []RepoSel   `json:"repos,omitempty"`
	Requirements   []ReqState  `json:"requirements,omitempty"`
	Answers        *gen.Answers      `json:"answers,omitempty"`
	AnswersRev     int               `json:"answers_rev,omitempty"`
	RoleOverrides  map[string]string `json:"role_overrides,omitempty"`
	Recommendations map[string]string `json:"recommendations,omitempty"` // campo → evidencia
	Archaeology    []ArchState `json:"archaeology,omitempty"`
	McpProbes      map[string]McpProbeState `json:"mcp_probes,omitempty"`
	SecretKeys     map[string]bool          `json:"secret_keys,omitempty"` // clave → guardada (presencia, jamás valores)
	CompletedAt    int64       `json:"completed_at,omitempty"`
}

// GHState — cómo hablamos con GitHub. El token JAMÁS vive aquí: pat → archivo
// 0600 en ConfigDir; gh → se le pide vivo a `gh auth token`.
type GHState struct {
	Mode string `json:"mode"` // gh | pat
	User string `json:"user"`
}

// RepoSel — un repo elegido para clonar (con ref opcional pineada).
type RepoSel struct {
	FullName string `json:"full_name"`
	Ref      string `json:"ref,omitempty"`
	Status   Status `json:"status"`
	Error    string `json:"error,omitempty"`
}

const schemaVersion = 1

// stepDef define el orden canónico. Los runners se registran por fase; un paso
// sin runner aún responde "no implementado" (el wizard crece fase a fase).
type stepDef struct {
	ID        string
	Title     string
	Skippable bool
}

var order = []stepDef{
	{"workspace", "Carpeta del workspace", false},
	{"github", "GitHub", false},
	{"clone", "Clonar repos", false},
	{"requirements", "Requisitos", false},
	{"discover", "Auto-discover", false},
	{"enrich", "Enriquecimiento (LLM)", true},
	{"generate", "Generar el harness", false},
	{"archaeology", "Arqueología (LLM)", true},
	{"mcps", "MCPs y secretos", true},
	{"first-task", "Primeras sesiones", true},
	{"finish", "Fin", false},
}

func defOf(id string) (stepDef, bool) {
	for _, d := range order {
		if d.ID == id {
			return d, true
		}
	}
	return stepDef{}, false
}

func freshSteps() []StepState {
	out := make([]StepState, 0, len(order))
	for _, d := range order {
		out = append(out, StepState{ID: d.ID, Title: d.Title, Status: Pending})
	}
	return out
}

// PublicState es lo que viaja en el snapshot (jamás incluye secretos).
type PublicState struct {
	Active        bool        `json:"active"`
	Step          string      `json:"step"`
	Steps         []StepState `json:"steps"`
	WorkspacePath string      `json:"workspace_path,omitempty"`
	Target        string      `json:"target,omitempty"`
	GitHub        *GHState    `json:"github,omitempty"`
	Repos         []RepoSel   `json:"repos,omitempty"`
	Requirements  []ReqState  `json:"requirements,omitempty"`
	Inventory       *gen.Inventory    `json:"inventory,omitempty"`
	Answers         *gen.Answers      `json:"answers,omitempty"`
	AnswersRev      int               `json:"answers_rev,omitempty"`
	RoleOverrides   map[string]string `json:"role_overrides,omitempty"`
	Recommendations map[string]string `json:"recommendations,omitempty"`
	Archaeology     []ArchState       `json:"archaeology,omitempty"`
	McpProbes       map[string]McpProbeState `json:"mcp_probes,omitempty"`
	SecretKeys      map[string]bool          `json:"secret_keys,omitempty"`
	CompletedAt     int64             `json:"completed_at,omitempty"`
}

// runner es la implementación de un paso. Se registran por fase de desarrollo
// en runnerFor(); ver steps.go.
type runner func(m *Manager) error
