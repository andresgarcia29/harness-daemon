package initflow

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ── paso clone: trae los repos elegidos a <ws>/repos/ ──
// Idempotente por ARTEFACTO: un repo ya clonado con el remote correcto se
// salta; matar el daemon a mitad de clone y reintentar retoma repo a repo.
// El token jamás toca argv: git nos re-invoca como GIT_ASKPASS (askpass.go).
//
// El núcleo (CloneRepos) es standalone: lo usa el Manager (local) y el
// subcomando `harness init-step clone` (el MISMO código corriendo en un VPS
// vía ssh — ADR-0011 §4).

// CloneRepos clona la selección. log recibe el progreso humano; onStatus el
// estado por repo (índice de sel). Devuelve cuántos fallaron.
func CloneRepos(ws, mode string, sel []RepoSel, log func(string), onStatus func(i int, s Status, errMsg string)) (int, error) {
	if len(sel) == 0 {
		return 0, errors.New("no hay repos seleccionados — elige al menos uno")
	}
	if mode == "" {
		return 0, errors.New("GitHub no configurado — completa el paso 2")
	}
	selfExe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	fails := 0
	for i, r := range sel {
		dest := filepath.Join(ws, "repos", filepath.Base(r.FullName))
		if cloneVerified(dest, r.FullName) {
			onStatus(i, OK, "")
			log(r.FullName + " ya clonado — verificado, se salta")
			continue
		}
		onStatus(i, Running, "")
		log("clonando " + r.FullName + "…")
		if err := cloneOne(selfExe, mode, r, dest, log); err != nil {
			fails++
			onStatus(i, Fail, err.Error())
			log("❌ " + r.FullName + ": " + err.Error())
			continue
		}
		onStatus(i, OK, "")
		log("✓ " + r.FullName)
	}
	if fails > 0 {
		return fails, fmt.Errorf("%d repo(s) fallaron — reintenta (los clonados no se repiten)", fails)
	}
	return 0, nil
}

func (m *Manager) runClone() error {
	m.mu.Lock()
	ws := m.st.Workspace
	sel := append([]RepoSel(nil), m.st.Repos...)
	mode := ""
	if m.st.GitHub != nil {
		mode = m.st.GitHub.Mode
	}
	m.mu.Unlock()
	if m.isRemote() {
		return m.remoteClone(ws, mode, sel)
	}
	_, err := CloneRepos(ws, mode, sel,
		func(s string) { m.logs.Append("clone", s) },
		func(i int, s Status, e string) { m.setRepoStatus(i, s, e) })
	return err
}

func cloneOne(selfExe, mode string, r RepoSel, dest string, log func(string)) error {
	_ = os.RemoveAll(dest + ".partial")
	url := "https://github.com/" + r.FullName + ".git"
	args := []string{"clone", "--progress", "--filter=blob:none", url, dest}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS="+selfExe,
		"HARNESS_ASKPASS=1",
		"HARNESS_ASKPASS_SOURCE="+mode,
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// git escribe el progreso con \r; troceamos por \r y \n para el log vivo
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	sc.Split(scanCRLF)
	last := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && line != last {
			log(line)
			last = line
		}
	}
	if err := cmd.Wait(); err != nil {
		_ = os.RemoveAll(dest) // un clone a medias no es un artefacto: fuera
		return fmt.Errorf("git clone falló (%v)", err)
	}
	if r.Ref != "" {
		out, err := exec.Command("git", "-C", dest, "checkout", "--detach", r.Ref).CombinedOutput()
		if err != nil {
			return fmt.Errorf("checkout %s: %s", r.Ref, strings.TrimSpace(string(out)))
		}
		log(r.FullName + " @ " + r.Ref)
	}
	return nil
}

// cloneVerified: el artefacto manda — .git existe y el remote apunta al repo.
func cloneVerified(dest, fullName string) bool {
	if fi, err := os.Stat(filepath.Join(dest, ".git")); err != nil || !fi.IsDir() {
		return false
	}
	out, err := exec.Command("git", "-C", dest, "remote", "get-url", "origin").Output()
	if err != nil {
		return false
	}
	u := strings.TrimSpace(string(out))
	return strings.Contains(u, fullName)
}

func (m *Manager) setRepoStatus(i int, s Status, errMsg string) {
	m.mu.Lock()
	if i >= 0 && i < len(m.st.Repos) {
		m.st.Repos[i].Status = s
		m.st.Repos[i].Error = errMsg
	}
	m.persistLocked()
	m.mu.Unlock()
}

// scanCRLF corta en \n Y \r (las líneas de progreso de git usan \r).
func scanCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
