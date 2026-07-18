package initflow

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/ghapi"
)

// ghStub crea un `gh` falso que imprime un token fijo.
func ghStub(t *testing.T, token string) {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "gh")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = auth ] && [ \"$2\" = token ]; then echo %s; exit 0; fi\nexit 1\n", token)
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HARNESS_GH_BIN", stub)
}

// apiStub monta un GitHub falso y apunta ghapi.APIBase ahí.
func apiStub(t *testing.T, wantToken string) {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+wantToken }
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("X-OAuth-Scopes", "repo, read:org")
		fmt.Fprint(w, `{"login":"andres"}`)
	})
	mux.HandleFunc("/user/orgs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"login":"corvux"}]`)
	})
	mux.HandleFunc("/orgs/corvux/repos", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"full_name":"corvux/atlas","name":"atlas","default_branch":"main","private":true,"language":"Go"}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := ghapi.APIBase
	ghapi.APIBase = srv.URL
	t.Cleanup(func() { ghapi.APIBase = old })
}

func TestGithubModoGH(t *testing.T) {
	setupEnv(t)
	ghStub(t, "ghtok123")
	apiStub(t, "ghtok123")
	m := New("t", (&adoptSpy{}).fn)
	res, code := m.Handle("github", map[string]any{"mode": "gh"})
	if code != 200 {
		t.Fatalf("gh: %d %v", code, res)
	}
	if m.Public().GitHub.User != "andres" || m.Public().GitHub.Mode != "gh" {
		t.Fatalf("estado github: %+v", m.Public().GitHub)
	}
	// el paso quedó ok
	for _, s := range m.Public().Steps {
		if s.ID == "github" && s.Status != OK {
			t.Fatalf("paso github: %s", s.Status)
		}
	}
}

func TestGithubModoPAT(t *testing.T) {
	setupEnv(t)
	apiStub(t, "pat456")
	m := New("t", (&adoptSpy{}).fn)
	// token malo → 400, nada guardado
	if _, code := m.Handle("github", map[string]any{"mode": "pat", "token": "malo"}); code != 400 {
		t.Fatal("token inválido debe rechazarse")
	}
	if _, err := os.Stat(tokenPath()); err == nil {
		t.Fatal("un token rechazado jamás se guarda")
	}
	res, code := m.Handle("github", map[string]any{"mode": "pat", "token": "pat456"})
	if code != 200 {
		t.Fatalf("pat: %d %v", code, res)
	}
	fi, err := os.Stat(tokenPath())
	if err != nil {
		t.Fatal("el PAT validado se guarda")
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("github-token debe ser 0600, es %v", fi.Mode().Perm())
	}
	// la respuesta jamás contiene el token
	if fmt.Sprintf("%v", res) == "" || fmt.Sprintf("%v", res) != "" && contains(fmt.Sprintf("%v", res), "pat456") {
		t.Fatal("el token no puede viajar de vuelta")
	}
	// orgs y repos con el token guardado
	if r, code := m.Handle("github-orgs", map[string]any{}); code != 200 {
		t.Fatalf("orgs: %v", r)
	} else if orgs := r.(map[string]any)["orgs"].([]string); len(orgs) != 2 || orgs[0] != "andres" || orgs[1] != "corvux" {
		t.Fatalf("orgs: %v", orgs)
	}
	if r, code := m.Handle("github-repos", map[string]any{"org": "corvux"}); code != 200 {
		t.Fatalf("repos: %v", r)
	} else if rs := r.(map[string]any)["repos"].([]ghapi.Repo); len(rs) != 1 || rs[0].FullName != "corvux/atlas" {
		t.Fatalf("repos: %v", rs)
	}
}

func contains(h, n string) bool { return len(h) >= len(n) && (h == n || len(h) > 0 && indexOf(h, n) >= 0) }
func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func TestRepoSelectValida(t *testing.T) {
	setupEnv(t)
	m := New("t", (&adoptSpy{}).fn)
	bad := [][]any{
		{},
		{map[string]any{"full_name": "-rf/algo"}},
		{map[string]any{"full_name": "a/b", "ref": "--upload-pack=/bin/sh"}},
		{map[string]any{"full_name": "a/b;rm"}},
		{map[string]any{"full_name": "x/same"}, map[string]any{"full_name": "y/same"}},
	}
	for i, b := range bad {
		if _, code := m.Handle("repos", map[string]any{"repos": b}); code != 400 {
			t.Fatalf("caso %d debió dar 400", i)
		}
	}
	if _, code := m.Handle("repos", map[string]any{"repos": []any{
		map[string]any{"full_name": "corvux/atlas", "ref": "v1.2.0"},
	}}); code != 200 {
		t.Fatal("selección válida")
	}
	if rs := m.Public().Repos; len(rs) != 1 || rs[0].Status != Pending {
		t.Fatalf("selección: %+v", rs)
	}
}

// TestCloneLocal clona desde un repo git LOCAL (sin red): file:// como URL no
// pasa por gitClone (que arma URL https), así que probamos las piezas: el
// verify por artefacto y el runner con un repo ya clonado a mano.
func TestCloneVerifyYResume(t *testing.T) {
	home := setupEnv(t)
	spy := &adoptSpy{}
	m := New("t", spy.fn)
	ws := filepath.Join(home, "ws")
	if _, code := m.Handle("workspace", map[string]any{"path": ws, "create": true}); code != 200 {
		t.Fatal("workspace")
	}
	// un "origen" git real local
	origin := filepath.Join(home, "origin", "atlas")
	mustGit(t, "", "init", "-q", origin)
	if err := os.WriteFile(filepath.Join(origin, "go.mod"), []byte("module atlas\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "add", ".")
	mustGit(t, origin, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	// clón manual en repos/ con remote que CONTIENE el full_name
	dest := filepath.Join(ws, "repos", "atlas")
	mustGit(t, "", "clone", "-q", origin, dest)
	mustGit(t, dest, "remote", "set-url", "origin", "https://github.com/corvux/atlas.git")

	if !cloneVerified(dest, "corvux/atlas") {
		t.Fatal("cloneVerified debe reconocer el artefacto")
	}
	if cloneVerified(dest, "corvux/otro") {
		t.Fatal("remote distinto NO verifica")
	}

	// selección + github pat "configurado" (token en archivo; no se usará
	// porque el único repo ya está clonado → se salta)
	if err := os.WriteFile(tokenPath(), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.st.GitHub = &GHState{Mode: "pat", User: "andres"}
	m.mu.Unlock()
	if _, code := m.Handle("repos", map[string]any{"repos": []any{
		map[string]any{"full_name": "corvux/atlas"},
	}}); code != 200 {
		t.Fatal("selección")
	}
	if _, code := m.Handle("step", map[string]any{"step": "clone", "action": "run"}); code != 200 {
		t.Fatal("run clone")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := m.Public()
		var cl *StepState
		for i := range st.Steps {
			if st.Steps[i].ID == "clone" {
				cl = &st.Steps[i]
			}
		}
		if cl.Status == OK {
			if st.Repos[0].Status != OK {
				t.Fatalf("repo: %+v", st.Repos[0])
			}
			break
		}
		if cl.Status == Fail {
			t.Fatalf("clone falló: %s", cl.Error)
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout esperando el clone")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Args = append([]string{"git", "-C", dir}, args...)
		cmd = exec.Command(cmd.Args[0], cmd.Args[1:]...)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
