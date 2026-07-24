package gen

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Answers — espejo tipado del contrato de harness-answers.yaml (esquema FIJO:
// doctor.sh y /harness-update lo parsean con grep/awk; la FORMA no se toca).
// Los campos que no viven en answers.yaml (deploy, principles, cronjobs) son
// insumos de otros artefactos (deploy-watch, constitution, cronjobs/) y el
// generador los mapea a sus destinos.
type Answers struct {
	Project struct {
		Name         string `json:"name"`
		TicketPrefix string `json:"ticket_prefix"`
	} `json:"project"`
	Instance struct {
		Repo string `json:"repo"` // "self" | URL
	} `json:"instance"`
	Flow   string `json:"flow"` // trunk-direct-to-prod | trunk-staging | prs
	Models struct {
		Provider    string `json:"provider"` // anthropic | vertex | bedrock | kimi | openrouter
		Architect   string `json:"architect"`
		Reviewer    string `json:"reviewer"`
		Implementer string `json:"implementer"`
		Mechanical  string `json:"mechanical"`
	} `json:"models"`
	LoopBudget      int    `json:"loop_budget"`
	MinionDecompose bool   `json:"minion_decompose"`
	Autonomy        string `json:"autonomy"` // full | checkpoint
	// UpstreamIssues — el canal de vuelta al plugin: auto (el agente verifica
	// y levanta el issue en harness-creator) | off (nada sale de la máquina).
	UpstreamIssues string    `json:"upstream_issues,omitempty"` // auto | off
	DAG            []string  `json:"dag"`
	Clusters       []Cluster `json:"clusters"`
	Capabilities   []CapSel  `json:"capabilities"`
	Secrets        struct {
		Source    string   `json:"source"`
		Refs      []string `json:"refs"`
		VaultAddr string   `json:"vault_addr,omitempty"`
		KvBase    string   `json:"kv_base,omitempty"`
	} `json:"secrets"`
	Tickets struct {
		Provider string `json:"provider"` // linear | github | none
	} `json:"tickets"`
	Memory struct {
		Provider string   `json:"provider"` // engram | none
		Profiles []string `json:"profiles"`
	} `json:"memory"`
	// ── fuera de answers.yaml: insumos de otros artefactos ──
	Deploy struct {
		Org          string `json:"org,omitempty"`
		ArgocdPrefix string `json:"argocd_prefix,omitempty"`
		KargoProject string `json:"kargo_project,omitempty"`
		CanaryTenant string `json:"canary_tenant,omitempty"`
		RollbackMode string `json:"rollback_mode,omitempty"` // auto | manual
	} `json:"deploy"`
	Principles []string `json:"principles"`
	Cronjobs   struct {
		Enabled bool     `json:"enabled"`
		Jobs    []string `json:"jobs"`
		Runner  string   `json:"runner,omitempty"` // crontab | gke | gha
	} `json:"cronjobs"`
}

type Cluster struct {
	Agent string   `json:"agent"`
	Kind  string   `json:"kind"` // service | infra | frontend | contracts
	Repos []string `json:"repos"`
	Owns  string   `json:"owns,omitempty"`
}

type CapSel struct {
	Name         string   `json:"name"`
	Bin          string   `json:"bin,omitempty"`
	Mcp          string   `json:"mcp,omitempty"`
	Tier         string   `json:"tier"`  // read-only | read-write | destructive
	Scope        string   `json:"scope"` // core | cronjob
	Profiles     []string `json:"profiles,omitempty"`
	ToolsAllowed []string `json:"tools_allowed,omitempty"` // nil = todas (se materializa como permissions.deny)
}

// ValidClusterKind — para validar propuestas (del LLM o del navegador).
func ValidClusterKind(k string) bool { return oneOf(k, "service", "infra", "frontend", "contracts") }

func oneOf(v string, opts ...string) bool {
	for _, o := range opts {
		if v == o {
			return true
		}
	}
	return false
}

// Validate — enums y coherencia mínima. Fail-closed: un answers inválido no
// genera nada.
func (a *Answers) Validate() error {
	var errs []string
	bad := func(f, msg string) { errs = append(errs, f+": "+msg) }
	if strings.TrimSpace(a.Project.Name) == "" {
		bad("project.name", "vacío")
	}
	if a.Flow != "" && !oneOf(a.Flow, "trunk-direct-to-prod", "trunk-staging", "prs") {
		bad("flow", a.Flow)
	}
	if a.Autonomy != "" && !oneOf(a.Autonomy, "full", "checkpoint") {
		bad("autonomy", a.Autonomy)
	}
	if a.UpstreamIssues != "" && !oneOf(a.UpstreamIssues, "auto", "off") {
		bad("upstream_issues", a.UpstreamIssues)
	}
	if a.LoopBudget < 0 || a.LoopBudget > 10 {
		bad("loop_budget", fmt.Sprint(a.LoopBudget))
	}
	if a.Tickets.Provider != "" && !oneOf(a.Tickets.Provider, "linear", "github", "none") {
		bad("tickets.provider", a.Tickets.Provider)
	}
	if a.Memory.Provider != "" && !oneOf(a.Memory.Provider, "engram", "none") {
		bad("memory.provider", a.Memory.Provider)
	}
	if a.Secrets.Source != "" && !oneOf(a.Secrets.Source,
		"vault", "gcp-secret-manager", "aws-secrets-manager", "doppler", "sops", "1password", "env") {
		bad("secrets.source", a.Secrets.Source)
	}
	if a.Deploy.RollbackMode != "" && !oneOf(a.Deploy.RollbackMode, "auto", "manual") {
		bad("deploy.rollback_mode", a.Deploy.RollbackMode)
	}
	if a.Cronjobs.Runner != "" && !oneOf(a.Cronjobs.Runner, "crontab", "gke", "gha") {
		bad("cronjobs.runner", a.Cronjobs.Runner)
	}
	seen := map[string]bool{}
	for _, c := range a.Clusters {
		if strings.TrimSpace(c.Agent) == "" {
			bad("clusters", "agent vacío")
			continue
		}
		if seen[c.Agent] {
			bad("clusters", "agente duplicado: "+c.Agent)
		}
		seen[c.Agent] = true
		if !oneOf(c.Kind, "service", "infra", "frontend", "contracts") {
			bad("clusters."+c.Agent+".kind", c.Kind)
		}
	}
	for _, c := range a.Capabilities {
		if c.Tier != "" && !oneOf(c.Tier, "read-only", "read-write", "destructive") {
			bad("capabilities."+c.Name+".tier", c.Tier)
		}
		if c.Scope != "" && !oneOf(c.Scope, "core", "cronjob") {
			bad("capabilities."+c.Name+".scope", c.Scope)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("answers inválido — %s", strings.Join(errs, " · "))
	}
	return nil
}

// ── seeding determinista (las reglas de clustering del SKILL, en código) ──

// SeedAnswers construye el borrador inicial desde el inventory: clustering por
// reglas, DAG por capas, secretos por hints. El LLM (enrich) REFINA esto; sin
// LLM, esto ya es un default honesto.
// NormalizeModels migra IDs crudos pre-v0.47 a aliases (fast|smart|deep) y
// asegura provider. Idempotente: aliases quedan igual; IDs desconocidos pasan
// tal cual (stamp-models reportará el error con remediación).
func NormalizeModels(a *Answers) {
	if a.Models.Provider == "" {
		a.Models.Provider = "anthropic"
	}
	// La migracion es POR ROL, no por modelo: un answers viejo con IDs
	// crudos declaraba jerarquia (caro/medio/barato), y la politica nueva
	// asigna esa jerarquia por funcion: architect piensa (deep), reviewer
	// e implementer producen (smart), mechanical despacha (fast). Un valor
	// que ya es alias pasa intacto.
	isAlias := func(s string) bool { return s == "fast" || s == "smart" || s == "deep" }
	migrate := func(v, def string) string {
		if v == "" || isAlias(v) {
			if v == "" {
				return def
			}
			return v
		}
		return def
	}
	a.Models.Architect = migrate(a.Models.Architect, "deep")
	a.Models.Reviewer = migrate(a.Models.Reviewer, "smart")
	a.Models.Implementer = migrate(a.Models.Implementer, "smart")
	a.Models.Mechanical = migrate(a.Models.Mechanical, "fast")
}

func SeedAnswers(inv *Inventory, wsPath string, overrides map[string]string) *Answers {
	a := &Answers{}
	a.Project.Name = filepath.Base(wsPath)
	a.Project.TicketPrefix = ticketPrefix(a.Project.Name)
	a.Instance.Repo = "self"
	a.Flow = "prs"
	if hasAny(inv, "argocd") || hasAny(inv, "kargo") {
		a.Flow = "trunk-direct-to-prod"
	}
	a.Models.Provider = "anthropic"
	a.Models.Architect = "deep"    // el pensador: plan, RFC, litigios (Fable)
	a.Models.Reviewer = "smart"    // el productor: veredictos y diffs (Opus)
	a.Models.Implementer = "smart" // el productor: todo el codigo (Opus)
	a.Models.Mechanical = "fast"   // lo especificisimo: digest, triage (Sonnet)
	a.LoopBudget = 3
	a.Autonomy = "checkpoint"
	a.UpstreamIssues = "auto"
	a.Clusters = SeedClusters(inv, overrides)
	a.DAG = SeedDAG(inv, overrides)
	if len(inv.SecretHints) > 0 {
		a.Secrets.Source = inv.SecretHints[0]
	} else {
		a.Secrets.Source = "env"
	}
	a.Tickets.Provider = "none"
	a.Memory.Provider = "engram"
	a.Memory.Profiles = []string{"orquestador", "arquitecto"}
	a.Deploy.RollbackMode = "auto"
	a.Cronjobs.Enabled = false
	a.Capabilities = SeedCapabilities(inv)
	return a
}

// SeedCapabilities — las capacidades CLI detectadas por señal (toolchains,
// gitleaks, ccusage…) entran al borrador desde el arranque: sin esto el
// bootstrap de la instancia no instalaba NADA (bug real: make init sin
// ensure lines). Los MCP se eligen a mano en su paso; phase 2 queda fuera.
func SeedCapabilities(inv *Inventory) []CapSel {
	caps, err := Catalog()
	if err != nil {
		return nil
	}
	rec := RecommendCapabilities(inv)
	var out []CapSel
	for _, c := range caps {
		if c.Provider != "cli" || c.Phase == 2 {
			continue
		}
		if _, ok := rec[c.Name]; !ok {
			continue
		}
		scope := "core"
		if c.Cronjob != "" {
			scope = "cronjob"
		}
		out = append(out, CapSel{Name: c.Name, Bin: c.Bin, Tier: c.PermissionTier,
			Scope: scope, Profiles: c.Profiles})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SeedClusters — las reglas de la tabla del SKILL: 1 abogado por service;
// UN infra para terraform/helm/ci; UN frontends para frontend+mobile;
// contracts/library/docs sin abogado. Techo 12: blando (avisa la UI).
func SeedClusters(inv *Inventory, overrides map[string]string) []Cluster {
	var out []Cluster
	var infra, fronts []string
	for _, r := range inv.Repos {
		switch inv.RoleOf(r.Name, overrides) {
		case "service":
			out = append(out, Cluster{Agent: "svc-" + r.Name, Kind: "service", Repos: []string{r.Name}})
		case "infra-module", "infra-live", "ci-library":
			infra = append(infra, r.Name)
		case "frontend", "mobile":
			fronts = append(fronts, r.Name)
		}
	}
	sort.Strings(infra)
	sort.Strings(fronts)
	if len(infra) > 0 {
		out = append(out, Cluster{Agent: "infra", Kind: "infra", Repos: infra})
	}
	if len(fronts) > 0 {
		out = append(out, Cluster{Agent: "frontends", Kind: "frontend", Repos: fronts})
	}
	return out
}

// SeedDAG — capas: contracts → libraries → services → frontends/mobile.
// Infra va primero de todo (los cimientos), docs/ci fuera del DAG.
func SeedDAG(inv *Inventory, overrides map[string]string) []string {
	layer := func(roles ...string) []string {
		var out []string
		for _, r := range inv.Repos {
			role := inv.RoleOf(r.Name, overrides)
			for _, want := range roles {
				if role == want {
					out = append(out, r.Name)
				}
			}
		}
		sort.Strings(out)
		return out
	}
	var dag []string
	dag = append(dag, layer("infra-module", "infra-live")...)
	dag = append(dag, layer("contracts")...)
	dag = append(dag, layer("library")...)
	dag = append(dag, layer("service")...)
	dag = append(dag, layer("frontend", "mobile")...)
	return dag
}

// RecommendCapabilities — el catálogo filtrado por las señales del inventory.
// Devuelve nombre→evidencia SOLO de lo detectado (o always/always-recommend).
func RecommendCapabilities(inv *Inventory) map[string]string {
	caps, err := Catalog()
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, c := range caps {
		if c.Phase == 2 {
			continue
		}
		if ev, ok := detectMatches(c.Detect, inv); ok {
			out[c.Name] = ev
		}
	}
	return out
}

// detectMatches interpreta el campo `detect` (texto humano con señal adentro)
// contra el inventory. Determinista: keywords → summary/signals.
func detectMatches(detect string, inv *Inventory) (string, bool) {
	d := strings.ToLower(detect)
	switch {
	case d == "always" || d == "always-recommend":
		return "recomendado siempre", true
	}
	type probe struct{ kw, sumKey, evidence string }
	probes := []probe{
		{"go.mod", "go", "repos Go: %s"},
		{"package.json", "typescript", "repos TS/JS: %s"},
		{"react", "typescript", "frontends: %s"},
		{"pyproject", "python", "repos Python: %s"},
		{"pubspec", "dart", "repos Flutter/Dart: %s"},
		{"terraform", "terraform", "repos terraform: %s"},
		{"buf", "proto", "contratos proto: %s"},
		{".proto", "proto", "contratos proto: %s"},
		{"helm", "helm", "charts helm: %s"},
		{"argocd", "argocd", "manifiestos argocd: %s"},
		{"kargo", "kargo", "kargo: %s"},
		{"workflows", "gha", "GitHub Actions: %s"},
		{"github actions", "gha", "GitHub Actions: %s"},
	}
	for _, p := range probes {
		if strings.Contains(d, p.kw) {
			if repos := inv.Summary[p.sumKey]; len(repos) > 0 {
				return fmt.Sprintf(p.evidence, strings.Join(repos, ", ")), true
			}
		}
	}
	// señales por repo (docker, compose, gcp…)
	for _, sig := range []string{"docker", "compose", "gcp", "k8s", "kubernetes"} {
		if strings.Contains(d, sig) {
			var hits []string
			for _, r := range inv.Repos {
				for _, s := range r.Signals {
					if s == sig || (sig == "kubernetes" && s == "k8s") {
						hits = append(hits, r.Name)
					}
				}
			}
			if len(hits) > 0 {
				return "señal " + sig + " en: " + strings.Join(hits, ", "), true
			}
		}
	}
	return "", false
}

func hasAny(inv *Inventory, key string) bool { return len(inv.Summary[key]) > 0 }

// EnsureServiceCoverage — la regla de cobertura POR CÓDIGO: todo repo con rol
// service debe tener abogado. Lo que el modelo omita se agrega como
// svc-<repo> con owns TBD. Devuelve los agregados (para loguearlo).
func EnsureServiceCoverage(clusters []Cluster, inv *Inventory, overrides map[string]string) ([]Cluster, []string) {
	if inv == nil {
		return clusters, nil
	}
	covered := map[string]bool{}
	for _, c := range clusters {
		for _, r := range c.Repos {
			covered[r] = true
		}
	}
	var added []string
	for _, r := range inv.Repos {
		if inv.RoleOf(r.Name, overrides) == "service" && !covered[r.Name] {
			clusters = append(clusters, Cluster{
				Agent: "svc-" + r.Name, Kind: "service", Repos: []string{r.Name},
				Owns: "TBD — confirmar con el equipo",
			})
			added = append(added, r.Name)
		}
	}
	return clusters, added
}

// MergeAnswers aplica un patch parcial (JSON del navegador) sobre el borrador:
// mapas se fusionan recursivo, arrays y escalares se REEMPLAZAN. El resultado
// se re-tipa contra el struct (campos desconocidos simplemente se ignoran).
func MergeAnswers(cur *Answers, patch map[string]any) (*Answers, error) {
	base, err := json.Marshal(cur)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	deepMerge(m, patch)
	merged, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	out := &Answers{}
	if err := json.Unmarshal(merged, out); err != nil {
		return nil, fmt.Errorf("patch no encaja en el esquema: %w", err)
	}
	return out, nil
}

func deepMerge(dst map[string]any, src map[string]any) {
	for k, v := range src {
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				deepMerge(dv, sv)
				continue
			}
		}
		dst[k] = v
	}
}

func ticketPrefix(name string) string {
	var out []rune
	for _, r := range name {
		if unicode.IsLetter(r) {
			out = append(out, unicode.ToUpper(r))
		}
		if len(out) == 3 {
			break
		}
	}
	if len(out) == 0 {
		return "TSK"
	}
	return string(out)
}
