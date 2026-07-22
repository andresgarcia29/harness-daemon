package gen

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// GenFile — una entrada de la tabla de generación (la Fase 3 del SKILL como
// código). Src es ruta dentro de assets/templates; Dst relativa al workspace.
type GenFile struct {
	Src    string
	Dst    string
	Mode   fs.FileMode
	Render bool
	When   func(a *Answers, inv *Inventory) bool
	// Extra agrega placeholders per-archivo (abogados, specs, k8s jobs).
	Extra map[string]string
	// Inline: contenido literal (sin Src).
	Inline []byte
	// Keep: documento de LEY — el template solo lo crea; una vez que existe,
	// evoluciona por arqueología/ratificación/humano y el re-render JAMÁS lo
	// pisa (la lección de corvux: un generate reescribió specs ratificadas
	// con el esqueleto). --force sigue pudiendo.
	Keep bool
}

func always(*Answers, *Inventory) bool { return true }

func hasCD(a *Answers, inv *Inventory) bool {
	return inv != nil && (len(inv.Summary["gha"]) > 0 || len(inv.Summary["argocd"]) > 0 || len(inv.Summary["kargo"]) > 0)
}

func hasFrontendOrCanary(a *Answers, inv *Inventory) bool {
	if a.Deploy.CanaryTenant != "" {
		return true
	}
	if inv == nil {
		return false
	}
	for _, r := range inv.Repos {
		if r.RoleGuess == "frontend" || r.RoleGuess == "mobile" {
			return true
		}
	}
	return false
}

func capChosen(a *Answers, name string) bool {
	for _, c := range a.Capabilities {
		if c.Name == name {
			return true
		}
	}
	return false
}

// Files construye la tabla completa para unos answers+inventory dados.
// El orden es estable (los tests de idempotencia dependen de ello).
func Files(a *Answers, inv *Inventory, o Opts) []GenFile {
	sh := fs.FileMode(0o755)
	reg := fs.FileMode(0o644)
	var out []GenFile
	add := func(g GenFile) { out = append(out, g) }

	// ── núcleo ──
	add(GenFile{Src: "README.md.tmpl", Dst: "README.md", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "CLAUDE.md.tmpl", Dst: "CLAUDE.md", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "AGENTS.md.tmpl", Dst: "AGENTS.md", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "skills/skill-creator/SKILL.md", Dst: ".claude/skills/skill-creator/SKILL.md", Mode: reg, When: always})
	add(GenFile{Src: "skills.yaml.tmpl", Dst: "skills.yaml", Mode: reg, Render: true, When: always, Keep: true})
	add(GenFile{Src: "manifest.yaml.tmpl", Dst: "manifest.yaml", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "harness-answers.yaml.tmpl", Dst: "harness-answers.yaml", Mode: reg, Render: true, When: always})
	add(GenFile{Inline: []byte(o.Version + "\n"), Dst: ".harness-version", Mode: reg, When: always})
	add(GenFile{Src: "Makefile.tmpl", Dst: "Makefile", Mode: reg, Render: true, When: always})
	add(GenFile{Inline: []byte("repos/\nworktrees/\nlocks/\n.cache/\n.secrets\n.secrets.d/\ninventory.json\n.harness/\ntasks/\n"), Dst: ".gitignore", Mode: reg, When: always})
	add(GenFile{Src: "models.yaml.tmpl", Dst: "models.yaml", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "policy.json", Dst: "harness-policy.json", Mode: reg, When: always})

	// ── .claude: settings, hooks, agentes, comandos ──
	add(GenFile{Src: "settings.json.tmpl", Dst: ".claude/settings.json", Mode: reg, Render: true, When: always})
	for _, h := range []string{"block-direct-push", "guard-canonical", "guard-build-slot", "track-read", "ui-emit"} {
		add(GenFile{Src: "hooks/" + h + ".sh", Dst: ".claude/hooks/" + h + ".sh", Mode: sh, When: always})
	}
	for _, ag := range []string{"architect", "implementer", "reviewer"} {
		add(GenFile{Src: "agents/" + ag + ".md.tmpl", Dst: ".claude/agents/" + ag + ".md", Mode: reg, Render: true, When: always})
	}
	add(GenFile{Src: "agents/qa.md.tmpl", Dst: ".claude/agents/qa.md", Mode: reg, Render: true, When: hasFrontendOrCanary})
	for _, c := range a.Clusters {
		c := c
		add(GenFile{
			Src: "agents/svc-agent.md.tmpl", Dst: ".claude/agents/" + c.Agent + ".md",
			Mode: reg, Render: true, When: always, Keep: true,
			Extra: map[string]string{
				"AGENT_NAME":    c.Agent,
				"CLUSTER_LABEL": c.Agent,
				"REPOS_CSV":     strings.Join(c.Repos, ", "),
				"OWNS":          defaultStr(c.Owns, "_TBD — arqueología/humano_"),
				"NOT_OWNS":      "_TBD — arqueología/humano_",
				"INVARIANTS":    "_TBD — arqueología/humano_",
			},
		})
	}
	for _, cmd := range []string{"feature", "rfc", "implement", "review", "ship", "promote", "archive", "auto"} {
		add(GenFile{Src: "commands/" + cmd + ".md.tmpl", Dst: ".claude/commands/" + cmd + ".md", Mode: reg, Render: true, When: always})
	}

	// ── docs y specs ──
	add(GenFile{Src: "docs/constitution.md.tmpl", Dst: "docs/constitution.md", Mode: reg, Render: true, When: always, Keep: true})
	add(GenFile{Src: "docs/index.md.tmpl", Dst: "docs/index.md", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "docs/architecture-map.md.tmpl", Dst: "docs/architecture/map.md", Mode: reg, Render: true, When: always, Keep: true})
	add(GenFile{Src: "docs/pipeline.md.tmpl", Dst: "docs/harness/pipeline.md", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "docs/intake.md.tmpl", Dst: "docs/harness/intake.md", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "docs/testing-policy.md.tmpl", Dst: "docs/harness/testing-policy.md", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "docs/evidence.md", Dst: "docs/harness/evidence.md", Mode: reg, When: always})
	add(GenFile{Src: "docs/policy.md", Dst: "docs/harness/policy.md", Mode: reg, When: always})
	add(GenFile{Src: "docs/minions-decomposition.md", Dst: "docs/harness/minions-decomposition.md", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "docs/quality.md.tmpl", Dst: "docs/quality.md", Mode: reg, Render: true, When: always})
	add(GenFile{Src: "docs/adr-template.md", Dst: "docs/adr/ADR-0000-template.md", Mode: reg, When: always})
	add(GenFile{Src: "docs/cronjobs.md.tmpl", Dst: "docs/harness/cronjobs.md", Mode: reg, Render: true,
		When: func(a *Answers, _ *Inventory) bool { return a.Cronjobs.Enabled }})
	for _, c := range a.Clusters {
		if c.Kind != "service" {
			continue
		}
		dom := strings.TrimPrefix(c.Agent, "svc-")
		add(GenFile{
			Src: "docs/spec.md.tmpl", Dst: "specs/" + dom + "/spec.md", Mode: reg, Render: true, When: always, Keep: true,
			Extra: map[string]string{
				"CAPABILITY":  dom,
				"OWNER_AGENT": c.Agent,
				"PREFIX":      strings.ToUpper(dom),
			},
		})
	}
	for _, keep := range []string{"docs/changelog/.gitkeep", "docs/services/.gitkeep", "scripts/smoke/.gitkeep"} {
		add(GenFile{Inline: []byte{}, Dst: keep, Mode: reg, When: always})
	}

	// ── scripts ──
	add(GenFile{Src: "scripts/bootstrap.sh.tmpl", Dst: "scripts/bootstrap.sh", Mode: sh, Render: true, When: always})
	add(GenFile{Src: "scripts/ship.sh.tmpl", Dst: "scripts/ship.sh", Mode: sh, Render: true, When: always})
	add(GenFile{Src: "scripts/evidence.py", Dst: "scripts/evidence.py", Mode: sh, When: always})
	add(GenFile{Src: "scripts/harness-policy.py", Dst: "scripts/harness-policy.py", Mode: sh, When: always})
	add(GenFile{Src: "scripts/secrets.sh.tmpl", Dst: "scripts/secrets.sh", Mode: sh, Render: true, When: always})
	add(GenFile{Src: "scripts/deploy-watch.sh.tmpl", Dst: "scripts/deploy-watch.sh", Mode: sh, Render: true, When: hasCD})
	add(GenFile{Src: "scripts/ticket-pull.sh.tmpl", Dst: "scripts/ticket-pull.sh", Mode: sh, Render: true,
		When: func(a *Answers, _ *Inventory) bool { return a.Tickets.Provider == "linear" }})
	add(GenFile{Src: "scripts/ticket-close.sh.tmpl", Dst: "scripts/ticket-close.sh", Mode: sh, Render: true,
		When: func(a *Answers, _ *Inventory) bool { return a.Tickets.Provider == "linear" }})
	for _, s := range []string{"worktree-task.sh", "quiet.sh", "with-secrets.sh", "emit.sh",
		"build-slot.sh", "gowork.sh", "py.sh", "fe.sh",
		"repo-brief.sh", "stamp-models.sh", "graph-refresh.sh",
		"pull-all.sh", "skills-sync.sh", "verdict-scaffold.sh", "minion-probe.sh"} {
		add(GenFile{Src: "scripts/" + s, Dst: "scripts/" + s, Mode: sh, When: always})
	}
	// doctor.sh: la copia autocontenida (viene de assets/scripts, no de templates)
	add(GenFile{Src: "@scripts/doctor.sh", Dst: "scripts/doctor.sh", Mode: sh, When: always})

	// ── panel ──
	for _, u := range []struct {
		src, dst string
		mode     fs.FileMode
	}{
		{"ui/panel.sh", "scripts/ui/panel.sh", sh},
		{"ui/server.py", "scripts/ui/server.py", sh},
		{"ui/pricing.json", "scripts/ui/pricing.json", reg},
	} {
		add(GenFile{Src: u.src, Dst: u.dst, Mode: u.mode, When: always})
	}

	// ── semgrep, cronjobs ──
	add(GenFile{Src: "semgrep-rules.yaml.tmpl", Dst: "semgrep/rules.yaml", Mode: reg, Render: true,
		When: func(a *Answers, _ *Inventory) bool { return capChosen(a, "semgrep") }})
	if a.Cronjobs.Enabled {
		add(GenFile{Src: "cronjobs/cron-runner.sh", Dst: "scripts/cronjobs/cron-runner.sh", Mode: sh, When: always})
		jobs := a.Cronjobs.Jobs
		sort.Strings(jobs)
		for _, j := range jobs {
			j := j
			add(GenFile{Src: "cronjobs/jobs/" + j + ".sh", Dst: "scripts/cronjobs/jobs/" + j + ".sh", Mode: sh, When: always})
			if a.Cronjobs.Runner == "gke" {
				add(GenFile{Src: "cronjobs/k8s-cronjob.yaml.tmpl", Dst: "k8s/cronjobs/" + j + ".yaml", Mode: reg, Render: true, When: always,
					Extra: map[string]string{"JOB_NAME": j, "SCHEDULE": cronSchedule(j)}})
			}
			if j == "ratchet-keeper" {
				add(GenFile{Inline: []byte("{}\n"), Dst: "ratchets.json", Mode: reg, When: always})
			}
		}
	}

	// filtra por condición
	final := out[:0]
	for _, g := range out {
		if g.When == nil || g.When(a, inv) {
			final = append(final, g)
		}
	}
	return final
}

// cronSchedule — horarios por defecto (documentados en docs/harness/cronjobs.md).
func cronSchedule(job string) string {
	m := map[string]string{
		"ci-doctor": "*/30 * * * *", "daily-digest": "0 7 * * *",
		"dep-shepherd": "0 6 * * 1", "vuln-watch": "0 5 * * *",
		"flake-warden": "0 4 * * *", "dead-code-reaper": "0 3 * * 0",
		"ratchet-keeper": "0 2 * * *", "mutation-sentinel": "0 1 * * 6",
		"doc-gardener": "0 8 * * 0", "slo-watchdog": "*/15 * * * *",
		"harness-janitor": "0 9 * * 0", "rule-miner": "0 10 1 * *",
	}
	if s, ok := m[job]; ok {
		return s
	}
	return "0 6 * * *"
}

// McpJSON construye .mcp.json desde el catálogo para los MCPs elegidos:
// wrap:true envuelve el comando en scripts/with-secrets.sh; engram fija
// --project <slug>.
func McpJSON(a *Answers) ([]byte, error) {
	servers := map[string]any{}
	for _, sel := range a.Capabilities {
		if sel.Mcp == "" {
			continue
		}
		cap, ok := CapByName(sel.Name)
		if !ok || cap.Config == nil {
			return nil, fmt.Errorf("MCP %s sin config en el catálogo", sel.Name)
		}
		cmd := cap.Config.Command
		args := make([]string, 0, len(cap.Config.Args))
		for _, arg := range cap.Config.Args {
			// el catálogo puede traer placeholders (engram: --project {{PROJECT_SLUG}})
			args = append(args, strings.ReplaceAll(arg, "{{PROJECT_SLUG}}", slugify(a.Project.Name)))
		}
		if cap.Wrap {
			args = append([]string{cmd}, args...)
			cmd = "scripts/with-secrets.sh"
		}
		servers[sel.Mcp] = map[string]any{"command": cmd, "args": args}
	}
	if len(servers) == 0 {
		return nil, nil
	}
	b, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
