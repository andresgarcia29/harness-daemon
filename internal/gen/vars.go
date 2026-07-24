package gen

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Opts — insumos del generador que no viven en answers.
type Opts struct {
	WS      string    // ruta del workspace
	Version string    // versión del harness (se estampa en .harness-version)
	Now     time.Time // inyectada para que los tests sean deterministas
	Force   bool      // pisar archivos personalizados
	DryRun  bool
}

// Vars construye el mapa global de placeholders. Los per-archivo (abogados,
// specs, cronjobs k8s) se agregan encima en el manifest.
func Vars(a *Answers, inv *Inventory, o Opts) map[string]string {
	slug := slugify(a.Project.Name)
	v := map[string]string{
		"PROJECT_NAME":       a.Project.Name,
		"PROJECT_SLUG":       slug,
		"TICKET_PREFIX":      a.Project.TicketPrefix,
		"HARNESS_VERSION":    o.Version,
		"GENERATED_AT":       o.Now.UTC().Format("2006-01-02T15:04:05Z"),
		"WORKSPACE":          o.WS,
		"INSTANCE_REPO":      yamlScalar(a.Instance.Repo),
		"FLOW":               a.Flow,
		"AUTONOMY":           a.Autonomy,
		"UPSTREAM_ISSUES":    defaultStr(a.UpstreamIssues, "auto"),
		"LOOP_BUDGET":        fmt.Sprint(a.LoopBudget),
		"MODEL_PROVIDER":     defaultStr(a.Models.Provider, "anthropic"),
		"MODEL_ARCHITECT":    a.Models.Architect,
		"MODEL_REVIEWER":     a.Models.Reviewer,
		"MODEL_IMPLEMENTER":  a.Models.Implementer,
		"MODEL_MECHANICAL":   a.Models.Mechanical,
		"MODEL_ESCALATION":   a.Models.Architect, // implementer escala al modelo de juicio
		"MEMORY_PROVIDER":    a.Memory.Provider,
		"MEMORY_PROFILES":    strings.Join(a.Memory.Profiles, ", "),
		"MEMORY_TOOL":        memoryTool(a),
		"TICKETS_PROVIDER":   a.Tickets.Provider,
		"SECRETS_SOURCE":     a.Secrets.Source,
		"VAULT_ADDR":         defaultStr(a.Secrets.VaultAddr, "https://TODO-tu-vault:8200"),
		"VAULT_KV_BASE":      defaultStr(a.Secrets.KvBase, slug+"/harness"),
		"VAULT_KEYS":         keysBlock(a, "vault"),
		"GCP_SM_KEYS":        keysBlock(a, "gcp"),
		"AWS_SM_KEYS":        keysBlock(a, "aws"),
		"SOPS_FILE":          "secrets.enc.env",
		"GITHUB_ORG":         defaultStr(a.Deploy.Org, orgFromInventory(inv)),
		"ARGO_APP_PREFIX":    defaultStr(a.Deploy.ArgocdPrefix, slug),
		"KARGO_PROJECT":      defaultStr(a.Deploy.KargoProject, slug),
		"CANARY_TENANT":      defaultStr(a.Deploy.CanaryTenant, "canary"),
		"ROLLBACK_MODE":      defaultStr(a.Deploy.RollbackMode, "auto"),
		"GCP_PROJECT":        "TODO-gcp-project",
		"NAMESPACE":          "harness",
		"HARNESS_IMAGE":      "ghcr.io/" + defaultStr(a.Deploy.Org, orgFromInventory(inv)) + "/harness-runner:latest",
		"HARNESS_REPO_URL":   harnessRepoURL(a),
		"DEP_ORDER":          strings.Join(a.DAG, " → "),
		"REPOS_CSV":          strings.Join(repoNames(inv), ", "),
		"DAG_LIST":           yamlList(a.DAG, "  "),
		"REPOS_LIST":         manifestRepos(a, inv),
		"CLUSTERS_LIST":      clustersYAML(a),
		"CAPABILITIES_LIST":  capabilitiesYAML(a),
		"SECRETS_REFS":       secretsRefsYAML(a),
		"ENSURE_LINES":       ensureLines(a),
		"REPO_TABLE":         repoTable(a, inv),
		"OWNERSHIP_TABLE":    ownershipTable(a),
		"QUALITY_TABLE":      qualityTable(inv),
		"PROJECT_PRINCIPLES": principles(a),
		"SECRETS_ONBOARDING": secretsOnboarding(a),
	}
	return v
}

func slugify(s string) string {
	out := strings.ToLower(strings.TrimSpace(s))
	out = strings.NewReplacer(" ", "-", "_", "-", ".", "-").Replace(out)
	return out
}

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func yamlScalar(s string) string {
	if s == "" {
		return "self"
	}
	return s
}

func memoryTool(a *Answers) string {
	if a.Memory.Provider == "engram" {
		return "engram (MCP)"
	}
	return "sin memoria episódica"
}

// keysBlock — las líneas dump_* de secrets.sh según la fuente. Sin refs
// registradas aún, un comentario honesto (el humano las agrega al conectar).
func keysBlock(a *Answers, kind string) string {
	if len(a.Secrets.Refs) == 0 {
		return "  # (agrega aquí tus claves — ver ejemplos abajo)"
	}
	var out []string
	for _, r := range a.Secrets.Refs {
		out = append(out, "  # ref registrada: "+r)
	}
	return strings.Join(out, "\n")
}

func orgFromInventory(inv *Inventory) string {
	if inv == nil {
		return "TODO-org"
	}
	for _, r := range inv.Repos {
		// remote tipo https://github.com/org/x o git@github.com:org/x
		s := r.Remote
		if i := strings.Index(s, "github.com"); i >= 0 {
			rest := strings.TrimLeft(s[i+len("github.com"):], ":/")
			if j := strings.IndexAny(rest, "/"); j > 0 {
				return rest[:j]
			}
		}
	}
	return "TODO-org"
}

func harnessRepoURL(a *Answers) string {
	if a.Instance.Repo != "" && a.Instance.Repo != "self" {
		return a.Instance.Repo
	}
	return "TODO-url-del-meta-repo"
}

func repoNames(inv *Inventory) []string {
	if inv == nil {
		return nil
	}
	var out []string
	for _, r := range inv.Repos {
		out = append(out, r.Name)
	}
	sort.Strings(out)
	return out
}

func yamlList(items []string, indent string) string {
	if len(items) == 0 {
		return indent + "[]"
	}
	var out []string
	for _, it := range items {
		out = append(out, indent+"- "+it)
	}
	return strings.Join(out, "\n")
}

// agentOf — qué abogado defiende un repo (por clusters del answers).
func agentOf(a *Answers, repo string) string {
	for _, c := range a.Clusters {
		for _, r := range c.Repos {
			if r == repo {
				return c.Agent
			}
		}
	}
	return "—"
}

func manifestRepos(a *Answers, inv *Inventory) string {
	if inv == nil || len(inv.Repos) == 0 {
		return "  []"
	}
	var out []string
	for _, r := range inv.Repos {
		out = append(out, fmt.Sprintf("  - name: %s\n    url: %s\n    branch: %s\n    kind: %s\n    agent: %s",
			r.Name, defaultStr(r.Remote, "TODO"), defaultStr(r.CurrentBranch, "main"), r.RoleGuess, agentOf(a, r.Name)))
	}
	return strings.Join(out, "\n")
}

func clustersYAML(a *Answers) string {
	if len(a.Clusters) == 0 {
		return "  []"
	}
	var out []string
	for _, c := range a.Clusters {
		s := fmt.Sprintf("  - agent: %s\n    kind: %s\n    repos: [%s]", c.Agent, c.Kind, strings.Join(c.Repos, ", "))
		if c.Owns != "" {
			s += fmt.Sprintf("\n    owns: %q", c.Owns)
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

func capabilitiesYAML(a *Answers) string {
	if len(a.Capabilities) == 0 {
		return "  []"
	}
	var out []string
	for _, c := range a.Capabilities {
		s := "  - name: " + c.Name
		if c.Bin != "" {
			s += "\n    bin: " + c.Bin
		}
		if c.Mcp != "" {
			s += "\n    mcp: " + c.Mcp
		}
		s += "\n    tier: " + defaultStr(c.Tier, "read-only")
		s += "\n    scope: " + defaultStr(c.Scope, "core")
		if len(c.Profiles) > 0 {
			s += "\n    profiles: [" + strings.Join(c.Profiles, ", ") + "]"
		}
		if len(c.ToolsAllowed) > 0 {
			s += "\n    # tools: " + strings.Join(c.ToolsAllowed, ", ")
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

func secretsRefsYAML(a *Answers) string {
	if len(a.Secrets.Refs) == 0 {
		return "    []"
	}
	return yamlList(a.Secrets.Refs, "    ")
}

// ensureLines — bootstrap: una línea ensure/require por capacidad CLI elegida,
// derivada del campo install del catálogo (brew → ensure; lo demás require).
// ensureLines — una línea `ensure`/`require` por capacidad elegida.
//
// La decisión la manda el catálogo (`install_kind`), NO una lectura del texto
// de `install`. La versión anterior solo reconocía comandos `brew`: npm, pip,
// go install, uv tool y gcloud components caían a `require`, el bootstrap se
// declaraba terminado sin instalarlos y el doctor los reportaba en ❌ con la
// remediación "corre scripts/bootstrap.sh", que ya se había corrido. Bucle sin
// salida para el usuario (harness-creator#23).
func ensureLines(a *Answers) string {
	var out []string
	for _, sel := range a.Capabilities {
		if sel.Bin == "" {
			continue
		}
		cap, ok := CapByName(sel.Name)
		if !ok || cap.Install == "" {
			out = append(out, fmt.Sprintf("require %s 'instálalo a mano'", sel.Bin))
			continue
		}
		cmd, kind := installFor(cap)
		if kind == "auto" {
			out = append(out, fmt.Sprintf("ensure %s %s", sel.Bin, cmd))
		} else {
			out = append(out, fmt.Sprintf("require %s '%s'", sel.Bin, cmd))
		}
		// post_install: idempotente y fail-open (ej. graphify registra su skill)
		if pi := strings.TrimSpace(cap.PostInstall); pi != "" {
			out = append(out, fmt.Sprintf("command -v %s >/dev/null && { %s || true; }", sel.Bin, pi))
		}
	}
	if len(out) == 0 {
		return "# (sin capacidades CLI adicionales elegidas)"
	}
	return strings.Join(out, "\n")
}

// installFor — el comando y el kind EFECTIVOS para esta plataforma.
func installFor(c Capability) (string, string) {
	cmd := strings.TrimSpace(c.Install)
	kind := strings.TrimSpace(c.InstallKind)
	if runtime.GOOS == "linux" && strings.TrimSpace(c.InstallLinux) != "" {
		cmd = strings.TrimSpace(c.InstallLinux)
		kind = strings.TrimSpace(c.InstallLinuxKind)
	}
	if kind != "auto" && kind != "manual" {
		kind = inferInstallKind(cmd) // catálogos viejos, sin el campo
	}
	return cmd, kind
}

// inferInstallKind — SOLO fallback para catálogos sin install_kind. La lista
// es cerrada a propósito: lo que no reconoce se verifica, no se ejecuta.
func inferInstallKind(cmd string) string {
	if strings.HasPrefix(cmd, "http://") || strings.HasPrefix(cmd, "https://") {
		return "manual"
	}
	head := cmd
	if i := strings.IndexByte(cmd, ' '); i > 0 {
		head = cmd[:i]
	}
	switch head {
	case "brew", "npm", "npx", "pnpm", "yarn", "pip", "pip3", "pipx", "uv",
		"go", "cargo", "gem", "apt-get", "apt", "dnf", "yum", "winget", "scoop", "gcloud":
		return "auto"
	}
	return "manual"
}

func repoTable(a *Answers, inv *Inventory) string {
	if inv == nil {
		return "| _sin inventario_ | | | |"
	}
	var out []string
	out = append(out, "| Repo | Rol | Lenguajes | Abogado |", "|---|---|---|---|")
	for _, r := range inv.Repos {
		out = append(out, fmt.Sprintf("| `%s` | %s | %s | %s |",
			r.Name, r.RoleGuess, strings.Join(r.Languages, ", "), agentOf(a, r.Name)))
	}
	return strings.Join(out, "\n")
}

func ownershipTable(a *Answers) string {
	var out []string
	out = append(out, "| Cluster | Repos | Posee |", "|---|---|---|")
	for _, c := range a.Clusters {
		owns := c.Owns
		if owns == "" {
			owns = "_TBD — arqueología/humano_"
		}
		out = append(out, fmt.Sprintf("| %s | %s | %s |", c.Agent, strings.Join(c.Repos, ", "), owns))
	}
	return strings.Join(out, "\n")
}

func qualityTable(inv *Inventory) string {
	if inv == nil {
		return ""
	}
	var out []string
	out = append(out, "| Repo | Build | Tests | Lint | Estado |", "|---|---|---|---|---|")
	for _, r := range inv.Repos {
		out = append(out, fmt.Sprintf("| `%s` | 🟡 | 🟡 | 🟡 | pendiente de arqueología |", r.Name))
	}
	return strings.Join(out, "\n")
}

func principles(a *Answers) string {
	if len(a.Principles) == 0 {
		return "1. _TBD — define 2-4 principios del proyecto (entrevista #11)_"
	}
	var out []string
	for i, p := range a.Principles {
		out = append(out, fmt.Sprintf("%d. %s", i+1, p))
	}
	return strings.Join(out, "\n")
}

func secretsOnboarding(a *Answers) string {
	switch a.Secrets.Source {
	case "vault":
		return fmt.Sprintf(`El token de Vault se pide en `+"`make init`"+` (interactivo, jamás por chat):
1. `+"`export VAULT_ADDR=%s`"+`
2. `+"`vault login -method=<oidc|userpass|github>`"+`
3. `+"`vault token create -period=768h -orphan`"+` → pégalo cuando bootstrap lo pida.
El token vive en `+"`~/.config/harness/vault-token`"+` (chmod 600) y su vigencia se valida en cada doctor.`, defaultStr(a.Secrets.VaultAddr, "https://TODO"))
	case "env":
		return "Exporta tus secretos como variables de entorno (ver `.secrets` de ejemplo) — `make init` valida presencia, jamás valores."
	default:
		return fmt.Sprintf("Fuente elegida: **%s**. `scripts/secrets.sh pull` materializa `.secrets` desde ahí; `make init` te guía con los comandos exactos de autenticación.", a.Secrets.Source)
	}
}
