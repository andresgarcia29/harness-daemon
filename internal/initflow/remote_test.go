package initflow

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// El stub de ssh ejecuta el "comando remoto" LOCALMENTE con sh -c: prueba el
// contrato entero (quoting, streams stderr/stdout, stdin) con el binario REAL
// de harness — solo el transporte es simulado.
func sshStub(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "ssh")
	script := `#!/bin/sh
for last; do :; done
exec sh -c "$last"
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HARNESS_SSH_BIN", stub)
}

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

// harnessBin compila el binario real una vez por corrida de tests.
func harnessBin(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "harness-bin")
		if err != nil {
			buildErr = err
			return
		}
		builtBin = filepath.Join(dir, "harness")
		cmd := exec.Command("go", "build", "-o", builtBin, "../../cmd/harnessd")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return builtBin
}

// El dry_run remoto valida con sh PURO: debe funcionar aunque el "VPS" no
// tenga harness (el bug del primer intento real: command not found por tecla).
func TestRemoteDryRunSinBinario(t *testing.T) {
	home := setupEnv(t)
	sshStub(t)
	// PATH mínimo: SIN harness (el VPS pelón)
	t.Setenv("PATH", "/usr/bin:/bin")
	m := New("t", (&adoptSpy{}).fn)
	m.SetTargetResolver(func(name string) (string, bool) { return "fake-vps", name == "corvux" })
	if _, code := m.Handle("target", map[string]any{"name": "corvux"}); code != 200 {
		t.Fatal("target")
	}
	res, code := m.Handle("workspace", map[string]any{"path": "~/nuevo-ws", "dry_run": true})
	if code != 200 {
		t.Fatalf("dry_run remoto sin binario debe funcionar: %d %v", code, res)
	}
	r := res.(map[string]any)
	if r["exists"] != false || r["writable"] != true || r["normalized"] != filepath.Join(home, "nuevo-ws") {
		t.Fatalf("probe: %v", r)
	}
	// ruta existente y con harness instalado → 409
	inst := filepath.Join(home, "ya-instalado")
	os.MkdirAll(inst, 0o755)
	os.WriteFile(filepath.Join(inst, ".harness-version"), []byte("1\n"), 0o644)
	if _, code := m.Handle("workspace", map[string]any{"path": "~/ya-instalado", "dry_run": true}); code != 409 {
		t.Fatal("workspace remoto ya instalado → 409")
	}
	// fuera del home sin confirmar → 400; con confirmar → 200
	if _, code := m.Handle("workspace", map[string]any{"path": "/tmp/x", "dry_run": true}); code != 400 {
		t.Fatal("fuera de home sin confirmación")
	}
	if _, code := m.Handle("workspace", map[string]any{"path": "/tmp/x", "dry_run": true, "confirm_outside_home": true}); code != 200 {
		t.Fatal("fuera de home confirmado")
	}
	// nada de esto ensució la bitácora del paso
	if tail := m.Logs().Tail("workspace", 50); len(tail) != 0 {
		t.Fatalf("el dry_run jamás loguea: %v", tail)
	}
}

func TestE2ERemotoConStubSSH(t *testing.T) {
	home := setupEnv(t)
	sshStub(t)
	apiStub(t, "pat456")
	// el "VPS" es esta máquina: harness en ~/.local/bin (remotePATH lo halla)
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(harnessBin(t))
	if err := os.WriteFile(filepath.Join(localBin, "harness"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	// claude stub (arqueología "remota") + bins de capacidades sembradas
	payload := `{\"owns\":\"dominio remoto\",\"not_owns\":\"nada\",\"invariants\":[\"i1\"],\"requirements\":[]}`
	claude := filepath.Join(localBin, "claude")
	if err := os.WriteFile(claude, []byte(fmt.Sprintf("#!/bin/sh\necho '{\"result\": \"%s\"}'\n", payload)), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"go", "gitleaks", "semgrep", "ccusage", "go-arch-lint", "gh"} {
		if err := os.WriteFile(filepath.Join(localBin, b),
			[]byte("#!/bin/sh\necho "+b+" 1.0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HARNESS_CLAUDE_BIN", claude)
	t.Setenv("PATH", localBin+":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin")

	spy := &adoptSpy{}
	m := New("t", spy.fn)
	m.SetTargetResolver(func(name string) (string, bool) {
		if name == "corvux" {
			return "fake-vps", true
		}
		return "", false
	})
	if _, code := m.Handle("target", map[string]any{"name": "corvux"}); code != 200 {
		t.Fatal("target")
	}

	// 1. workspace REMOTO — jamás adopta local
	ws := filepath.Join(home, "remote-ws")
	if r, code := m.Handle("workspace", map[string]any{"path": ws, "create": true}); code != 200 {
		t.Fatalf("workspace remoto: %v", r)
	}
	if len(spy.calls) != 0 {
		t.Fatal("un workspace remoto JAMÁS se adopta localmente")
	}
	if fi, err := os.Stat(filepath.Join(ws, "repos")); err != nil || !fi.IsDir() {
		t.Fatal("el init-step remoto debe crear repos/")
	}
	if m.Public().Step != "github" {
		t.Fatalf("paso: %s", m.Public().Step)
	}

	// 2. github PAT: valida local, token al "VPS" por stdin
	if r, code := m.Handle("github", map[string]any{"mode": "pat", "token": "pat456"}); code != 200 {
		t.Fatalf("github remoto: %v", r)
	}
	if _, err := os.Stat(tokenPath()); err != nil {
		t.Fatal("el token debe quedar en el ConfigDir del VPS")
	}

	// 3. clone: pre-clonado en el "VPS" → CloneRepos remoto verifica y salta
	origin := filepath.Join(home, "origin", "atlas")
	mustGit(t, "", "init", "-q", origin)
	os.MkdirAll(filepath.Join(origin, "cmd"), 0o755)
	os.WriteFile(filepath.Join(origin, "go.mod"), []byte("module atlas\n"), 0o644)
	os.WriteFile(filepath.Join(origin, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	mustGit(t, origin, "add", ".")
	mustGit(t, origin, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	dest := filepath.Join(ws, "repos", "atlas")
	mustGit(t, "", "clone", "-q", origin, dest)
	mustGit(t, dest, "remote", "set-url", "origin", "https://github.com/corvux/atlas.git")
	if _, code := m.Handle("repos", map[string]any{"repos": []any{map[string]any{"full_name": "corvux/atlas"}}}); code != 200 {
		t.Fatal("selección")
	}
	runStepOK(t, m, "clone")
	if st := m.Public(); st.Repos[0].Status != OK {
		t.Fatalf("repo remoto: %+v", st.Repos[0])
	}

	// 4-6. requirements → discover → skip enrich → generate (todo "allá")
	runStepOK(t, m, "requirements")
	runStepOK(t, m, "discover")
	if p := m.Public(); p.Inventory == nil || p.Answers == nil {
		t.Fatal("el inventory remoto debe poblar el borrador local")
	}
	if _, code := m.Handle("step", map[string]any{"step": "enrich", "action": "skip"}); code != 200 {
		t.Fatal("skip enrich")
	}
	runStepOK(t, m, "generate")
	if _, err := os.Stat(filepath.Join(ws, "harness-answers.yaml")); err != nil {
		t.Fatal("generate remoto debe escribir en el ws del VPS")
	}

	// 7-9. archaeology → mcps → first-task → finish
	runStepOK(t, m, "archaeology")
	if st := m.Public(); len(st.Archaeology) != 1 || st.Archaeology[0].Status != OK {
		t.Fatalf("arqueología remota: %+v", st.Archaeology)
	}
	runStepOK(t, m, "mcps")
	os.MkdirAll(filepath.Join(ws, "tasks", "T-1"), 0o755)
	runStepOK(t, m, "first-task")
	runStepOK(t, m, "finish")
	if p := m.Public(); p.Active || p.CompletedAt == 0 {
		t.Fatalf("finish remoto: %+v", p)
	}
	// reanudación en remoto: el estado vive en ConfigDir, sin espejo local
	if _, err := os.Stat(filepath.Join(ws, ".harness", "init", "state.json")); err == nil {
		// el ws remoto SÍ tiene su state? No: el manager local no lo escribe;
		// pero el init-step remoto pudo crear .harness/init (discover.sh) — lo
		// que NO debe existir es el state.json del manager
		t.Fatal("el state del manager jamás se escribe en la ruta remota")
	}

	// tras finish el plano está apagado: TODO da 410, incluido target
	if _, code := m.Handle("target", map[string]any{"name": ""}); code != 410 {
		t.Fatal("tras finish todo es 410")
	}
}
