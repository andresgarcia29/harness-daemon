package gen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Los tres bugs que una instalación real cazó en el generador
// (harness-creator#23, #24, #27) y el MCP remoto que el catálogo ya trae.

func TestEnsureLinesUsaInstallKind(t *testing.T) {
	// Una capacidad por package manager de los que ANTES caían a require:
	// el bootstrap se declaraba terminado sin instalarlos y el doctor mandaba
	// a correr el bootstrap otra vez. Bucle sin salida (harness-creator#23).
	a := &Answers{Capabilities: []CapSel{
		{Name: "dependency-cruiser", Bin: "depcruise"}, // npm i -g
		{Name: "import-linter", Bin: "lint-imports"},   // uv tool install
		{Name: "go-arch-lint", Bin: "go-arch-lint"},    // go install
		{Name: "kubectl", Bin: "kubectl"},              // gcloud components
		{Name: "graphify", Bin: "graphify"},            // uv tool install
		{Name: "jq", Bin: "jq"},                        // brew
		{Name: "flutter-toolchain", Bin: "flutter"},    // URL: manual
	}}
	got := ensureLines(a)
	for _, bin := range []string{"depcruise", "lint-imports", "go-arch-lint", "kubectl", "graphify", "jq"} {
		if !strings.Contains(got, "ensure "+bin+" ") {
			t.Errorf("%s debería instalarse (ensure), quedó:\n%s", bin, got)
		}
	}
	if !strings.Contains(got, "require flutter ") {
		t.Errorf("flutter es una URL: debe ser require, quedó:\n%s", got)
	}
	// post_install: idempotente y fail-open (graphify registra su skill)
	if !strings.Contains(got, "graphify install") {
		t.Errorf("post_install del catálogo ignorado, quedó:\n%s", got)
	}
	// nada de lo emitido puede romper la sintaxis del bootstrap
	for _, meta := range []string{"(", ")", "|", "&&", ";"} {
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "ensure ") && strings.Contains(line, meta) {
				t.Errorf("línea ensure con sintaxis de shell (%s): %q", meta, line)
			}
		}
	}
}

func TestInferInstallKindEsElFallback(t *testing.T) {
	// Para catálogos viejos sin install_kind. Lista cerrada: lo que no
	// reconoce se VERIFICA, no se ejecuta.
	auto := []string{"brew install jq", "npm i -g knip", "uv tool install semble",
		"go install x@latest", "pip install y", "gcloud components install kubectl"}
	for _, c := range auto {
		if k := inferInstallKind(c); k != "auto" {
			t.Errorf("%q: esperaba auto, fue %s", c, k)
		}
	}
	manual := []string{"https://docs.flutter.dev/get-started", "http://x", "self-hosted helm chart", ""}
	for _, c := range manual {
		if k := inferInstallKind(c); k != "manual" {
			t.Errorf("%q: esperaba manual, fue %s", c, k)
		}
	}
	// install_kind del catálogo GANA sobre la inferencia
	if _, k := installFor(Capability{Install: "npm i -D @stryker-mutator/core", InstallKind: "manual"}); k != "manual" {
		t.Errorf("install_kind del catálogo ignorado: %s", k)
	}
}

func TestGitignoreSaleDelTemplate(t *testing.T) {
	// Mientras fue una lista embebida en el generador se divergió del
	// instalador: faltaban graphify-out/ (128 MB del grafo entrando a un
	// git add -A), go.work y go.work.sum (harness-creator#27).
	t.Setenv("HARNESS_CONFIG_DIR", t.TempDir())
	ws := t.TempDir()
	inv := fixtureInv()
	if _, err := Generate(fixtureAnswers(inv, ws), inv, opts(ws)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(ws, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, entry := range []string{"repos/", "worktrees/", "locks/", ".cache/", ".secrets",
		".secrets.d/", "inventory.json", "go.work", "go.work.sum", "graphify-out/", ".harness/", "tasks/"} {
		if !lineIn(got, entry) {
			t.Errorf(".gitignore sin %q (regenerable o local: no va a git)", entry)
		}
	}
}

func lineIn(body, want string) bool {
	for _, l := range strings.Split(body, "\n") {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

func TestMcpRemotoNoSeGeneraVacio(t *testing.T) {
	// El catálogo trae MCPs remotos (type: http + url). Asumir solo la forma
	// local escribía {"command":"","args":[]}: un .mcp.json roto en silencio.
	var remoto string
	caps, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range caps {
		if c.Provider == "mcp" && c.Config != nil && c.Config.URL != "" {
			remoto = c.Name
			break
		}
	}
	if remoto == "" {
		t.Skip("el catálogo embebido no trae MCPs remotos")
	}
	cap, _ := CapByName(remoto)
	a := &Answers{Capabilities: []CapSel{{Name: remoto, Mcp: cap.Mcp}}}
	b, err := McpJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		McpServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	e := doc.McpServers[cap.Mcp]
	if e["url"] != cap.Config.URL {
		t.Errorf("MCP remoto sin url: %+v", e)
	}
	if _, hasCmd := e["command"]; hasCmd {
		t.Errorf("MCP remoto con command (no hay nada que ejecutar): %+v", e)
	}
}

func TestUpstreamIssuesTieneValor(t *testing.T) {
	// El template del answers trae {{UPSTREAM_ISSUES}} y Render es
	// fail-closed: sin este valor, `harness generate` muere entero.
	a := &Answers{}
	if v := Vars(a, nil, opts("/ws"))["UPSTREAM_ISSUES"]; v != "auto" {
		t.Errorf("default de upstream_issues: esperaba auto, fue %q", v)
	}
	a.UpstreamIssues = "off"
	if v := Vars(a, nil, opts("/ws"))["UPSTREAM_ISSUES"]; v != "off" {
		t.Errorf("upstream_issues elegido por el humano se perdió: %q", v)
	}
	if err := a.Validate(); err != nil && strings.Contains(err.Error(), "upstream_issues") {
		t.Errorf("off debería ser válido: %v", err)
	}
	a.UpstreamIssues = "quizás"
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "upstream_issues") {
		t.Errorf("un valor inválido debe rechazarse, err=%v", err)
	}
}

// TestManifestCubreLoQueElDoctorExige — el guardián de la clase entera de
// bugs: el doctor del instalador exige N scripts, el manifiesto del generador
// los emite, y las dos listas se mantenían a mano. Cada vez que el instalador
// sumó uno (plan-lint.sh, harness-bug.sh), `harness generate` produjo un
// workspace que fallaba su PROPIO doctor. Aquí se leen las dos del código real
// y se intersectan: cero opinión, y la próxima vez falla en CI, no en la
// máquina de alguien.
func TestManifestCubreLoQueElDoctorExige(t *testing.T) {
	doctor, err := Asset("scripts/doctor.sh")
	if err != nil {
		t.Fatal(err)
	}
	// el bloque: for s in a.sh b.sh …; do  (scripts de instancia ejecutables)
	body := string(doctor)
	i := strings.Index(body, "for s in ")
	if i < 0 {
		t.Fatal("no encuentro la lista de scripts exigidos en doctor.sh")
	}
	j := strings.Index(body[i:], "; do")
	if j < 0 {
		t.Fatal("lista de scripts sin cierre en doctor.sh")
	}
	required := strings.Fields(strings.TrimPrefix(body[i:i+j], "for s in "))

	ws := t.TempDir()
	inv := fixtureInv()
	emitted := map[string]bool{}
	for _, g := range Files(fixtureAnswers(inv, ws), inv, opts(ws)) {
		emitted[strings.TrimPrefix(g.Dst, "scripts/")] = true
	}
	for _, r := range required {
		if !strings.HasSuffix(r, ".sh") {
			continue
		}
		if !emitted[r] {
			t.Errorf("doctor.sh exige scripts/%s y el manifiesto del generador NO lo emite: "+
				"toda instalación nueva fallaría su propio doctor", r)
		}
	}
}
