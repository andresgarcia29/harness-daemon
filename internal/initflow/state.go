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
	CompletedAt    int64       `json:"completed_at,omitempty"`
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
	CompletedAt   int64       `json:"completed_at,omitempty"`
}

// runner es la implementación de un paso. Se registran por fase de desarrollo
// en runnerFor(); ver steps.go.
type runner func(m *Manager) error
