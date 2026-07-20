package gen

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func fixtureInv() *Inventory {
	return &Inventory{
		Workspace: "/ws", RepoCount: 3,
		Repos: []InvRepo{
			{Name: "atlas", RoleGuess: "service", Languages: []string{"go"}, Signals: []string{"docker", "gha"}, Remote: "git@github.com:corvux/atlas.git", CurrentBranch: "main"},
			{Name: "webapp", RoleGuess: "frontend", Languages: []string{"typescript"}, Signals: []string{"gha"}, Remote: "https://github.com/corvux/webapp", CurrentBranch: "main"},
			{Name: "tf-core", RoleGuess: "infra-module", Languages: []string{}, Signals: []string{"gcp"}, Remote: "git@github.com:corvux/tf-core.git", CurrentBranch: "main"},
		},
		SecretHints: []string{"env"},
		ByRole:      map[string][]string{"service": {"atlas"}, "frontend": {"webapp"}, "infra-module": {"tf-core"}},
		Summary:     map[string][]string{"go": {"atlas"}, "typescript": {"webapp"}, "terraform": {"tf-core"}, "gha": {"atlas", "webapp"}},
	}
}

func fixtureAnswers(inv *Inventory, ws string) *Answers {
	a := SeedAnswers(inv, ws, nil)
	a.Project.Name = "corvux"
	a.Project.TicketPrefix = "COR"
	a.Capabilities = []CapSel{
		{Name: "jq", Bin: "jq", Tier: "read-only", Scope: "core"},
		{Name: "gh", Bin: "gh", Tier: "read-write", Scope: "core"},
		{Name: "serena", Mcp: "serena", Tier: "read-write", Scope: "core", Profiles: []string{"implementer"}},
		{Name: "engram", Mcp: "engram", Tier: "read-write", Scope: "core"},
	}
	a.Cronjobs.Enabled = true
	a.Cronjobs.Jobs = []string{"ci-doctor", "daily-digest", "ratchet-keeper"}
	a.Cronjobs.Runner = "crontab"
	a.Principles = []string{"multi-tenancy primero", "contratos expand/contract"}
	return a
}

func opts(ws string) Opts {
	return Opts{WS: ws, Version: "9.9.9-test", Now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
}

func TestGenerateCompleto(t *testing.T) {
	t.Setenv("HARNESS_CONFIG_DIR", t.TempDir())
	ws := t.TempDir()
	inv := fixtureInv()
	a := fixtureAnswers(inv, ws)
	rep, err := Generate(a, inv, opts(ws))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created < 40 {
		t.Fatalf("se esperaban ~45+ archivos, creados %d", rep.Created)
	}
	// 1. ningún placeholder sobrevive
	ph := regexp.MustCompile(`\{\{[A-Z_]+\}\}`)
	filepath.WalkDir(ws, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.Contains(p, "scripts/ui/dist") {
			return nil
		}
		// el placeholder del op-token del panel es DELIBERADO (lo inyecta el server)
		b, _ := os.ReadFile(p)
		for _, m := range ph.FindAll(b, -1) {
			if string(m) == "{{VAULT_KV_BASE}}" && strings.Contains(p, "secrets.sh") {
				continue // vive en comentarios de ejemplo del template… no: debe estar resuelto
			}
			t.Errorf("%s: placeholder sin resolver %s", strings.TrimPrefix(p, ws), m)
		}
		return nil
	})
	// 2. artefactos núcleo
	for _, f := range []string{"CLAUDE.md", "harness-answers.yaml", ".harness-version", "Makefile",
		".claude/settings.json", ".claude/hooks/block-direct-push.sh", ".claude/agents/architect.md",
		".claude/agents/svc-atlas.md", ".claude/agents/frontends.md", ".claude/agents/qa.md",
		".claude/commands/auto.md", "scripts/ship.sh", "scripts/doctor.sh", "scripts/bootstrap.sh",
		"scripts/evidence.py", "scripts/harness-policy.py", "harness-policy.json",
		"specs/atlas/spec.md", "docs/constitution.md", ".mcp.json", "scripts/ui/panel.sh",
		"scripts/ui/dist/index.html", "scripts/cronjobs/jobs/ci-doctor.sh", "ratchets.json",
		"docs/harness/evidence.md", "docs/harness/policy.md"} {
		if _, err := os.Stat(filepath.Join(ws, f)); err != nil {
			t.Errorf("falta %s", f)
		}
	}
	// version estampada
	if b, _ := os.ReadFile(filepath.Join(ws, ".harness-version")); strings.TrimSpace(string(b)) != "9.9.9-test" {
		t.Errorf(".harness-version: %q", b)
	}
	// 3. bits de ejecución
	for _, f := range []string{"scripts/ship.sh", "scripts/doctor.sh", "scripts/evidence.py", "scripts/harness-policy.py", ".claude/hooks/block-direct-push.sh", "scripts/cronjobs/cron-runner.sh"} {
		fi, err := os.Stat(filepath.Join(ws, f))
		if err != nil || fi.Mode()&0o111 == 0 {
			t.Errorf("%s debe ser ejecutable", f)
		}
	}
	// 4. .mcp.json: engram con --project y wrap donde toca
	mcp, _ := os.ReadFile(filepath.Join(ws, ".mcp.json"))
	if !bytes.Contains(mcp, []byte(`"--project"`)) || !bytes.Contains(mcp, []byte("corvux")) {
		t.Errorf("engram debe fijar --project <slug>: %s", mcp)
	}
	// 5. bash -n sobre los scripts generados
	for _, f := range []string{"scripts/ship.sh", "scripts/bootstrap.sh", "scripts/secrets.sh", "scripts/doctor.sh"} {
		if out, err := exec.Command("bash", "-n", filepath.Join(ws, f)).CombinedOutput(); err != nil {
			t.Errorf("bash -n %s: %s", f, out)
		}
	}
}

func TestGenerateIdempotente(t *testing.T) {
	t.Setenv("HARNESS_CONFIG_DIR", t.TempDir())
	ws := t.TempDir()
	inv := fixtureInv()
	a := fixtureAnswers(inv, ws)
	if _, err := Generate(a, inv, opts(ws)); err != nil {
		t.Fatal(err)
	}
	snap1 := hashTree(t, ws)
	rep2, err := Generate(a, inv, opts(ws))
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Created != 0 || rep2.Updated != 0 || rep2.Conflicts != 0 {
		t.Fatalf("segunda corrida debe ser todo kept: %+v", rep2)
	}
	if snap2 := hashTree(t, ws); snap1 != snap2 {
		t.Fatal("generar dos veces debe ser byte-idéntico")
	}
}

func TestGenerateRespetaPersonalizaciones(t *testing.T) {
	t.Setenv("HARNESS_CONFIG_DIR", t.TempDir())
	ws := t.TempDir()
	inv := fixtureInv()
	a := fixtureAnswers(inv, ws)
	if _, err := Generate(a, inv, opts(ws)); err != nil {
		t.Fatal(err)
	}
	// el humano personaliza un doc NORMAL (pipeline) y uno de LEY (constitution)
	custom := filepath.Join(ws, "docs", "harness", "pipeline.md")
	if err := os.WriteFile(custom, []byte("MI PIPELINE LOCAL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	law := filepath.Join(ws, "docs", "constitution.md")
	if err := os.WriteFile(law, []byte("MI LEY RATIFICADA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// cambia un answer y regenera
	a.Project.TicketPrefix = "CVX"
	rep, err := Generate(a, inv, opts(ws))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Conflicts == 0 {
		t.Fatalf("la personalización debe reportarse como conflict: %+v", rep)
	}
	if b, _ := os.ReadFile(custom); string(b) != "MI PIPELINE LOCAL\n" {
		t.Fatal("el archivo del humano JAMÁS se pisa sin --force")
	}
	if _, err := os.Stat(custom + ".new"); err != nil {
		t.Fatal("la propuesta nueva debe quedar en .new")
	}
	// los docs de LEY (Keep): intactos SIEMPRE, sin .new siquiera — el
	// template solo los crea; la lección de las specs ratificadas de corvux
	if b, _ := os.ReadFile(law); string(b) != "MI LEY RATIFICADA\n" {
		t.Fatal("un doc de ley existente jamás se re-renderiza")
	}
	if _, err := os.Stat(law + ".new"); err == nil {
		t.Fatal("la ley ni siquiera genera .new — evoluciona por arqueología/firma/humano")
	}
	// con --force sí se pisa (ambos)
	o := opts(ws)
	o.Force = true
	if _, err := Generate(a, inv, o); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(custom); string(b) == "MI PIPELINE LOCAL\n" {
		t.Fatal("--force debe pisar")
	}
	if b, _ := os.ReadFile(law); string(b) == "MI LEY RATIFICADA\n" {
		t.Fatal("--force pisa también la ley (explícito y a sabiendas)")
	}
}

func TestAnswersYAMLRoundtrip(t *testing.T) {
	t.Setenv("HARNESS_CONFIG_DIR", t.TempDir())
	ws := t.TempDir()
	inv := fixtureInv()
	a := fixtureAnswers(inv, ws)
	if _, err := Generate(a, inv, opts(ws)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(ws, "harness-answers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseAnswersYAML(b)
	if err != nil {
		t.Fatalf("el answers generado debe parsear con el parser fijo: %v", err)
	}
	if got.Project.Name != "corvux" || got.Project.TicketPrefix != "COR" ||
		got.Flow != a.Flow || got.Autonomy != a.Autonomy || got.LoopBudget != a.LoopBudget {
		t.Fatalf("roundtrip escalares: %+v", got)
	}
	if len(got.DAG) != len(a.DAG) || len(got.Clusters) != len(a.Clusters) || len(got.Capabilities) != len(a.Capabilities) {
		t.Fatalf("roundtrip listas: dag=%d clusters=%d caps=%d", len(got.DAG), len(got.Clusters), len(got.Capabilities))
	}
	for i, c := range got.Clusters {
		if c.Agent != a.Clusters[i].Agent || c.Kind != a.Clusters[i].Kind || len(c.Repos) != len(a.Clusters[i].Repos) {
			t.Fatalf("cluster %d: %+v vs %+v", i, c, a.Clusters[i])
		}
	}
	if got.Secrets.Source != a.Secrets.Source || got.Memory.Provider != "engram" || len(got.Memory.Profiles) != 2 {
		t.Fatalf("roundtrip secrets/memory: %+v", got)
	}
}

func hashTree(t *testing.T, ws string) string {
	t.Helper()
	h := sha256.New()
	filepath.WalkDir(ws, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		h.Write([]byte(strings.TrimPrefix(p, ws)))
		h.Write(b)
		return nil
	})
	return string(h.Sum(nil))
}
