package initflow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/andresgarcia29/harness-daemon/internal/ghapi"
	"github.com/andresgarcia29/harness-daemon/internal/ident"
)

// ── paso github: autenticación (gh detectado o PAT pegado) ──
// El paso se completa por acción (como workspace), no por runner: validar un
// token es síncrono y rápido.

func tokenPath() string { return filepath.Join(ident.ConfigDir(), "github-token") }

func (m *Manager) handleGithub(body map[string]any) (any, int) {
	switch str(body, "mode") {
	case "gh":
		_, user, err := ghapi.Detect()
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, 400
		}
		m.mu.Lock()
		m.st.GitHub = &GHState{Mode: "gh", User: user}
		m.logs.Append("github", "gh autenticado como "+user)
		m.setStepLocked("github", OK, "gh · "+user, "")
		m.mu.Unlock()
		return map[string]any{"ok": true, "mode": "gh", "user": user}, 200
	case "pat":
		tok := str(body, "token")
		if tok == "" {
			return map[string]any{"ok": false, "error": "token vacío"}, 400
		}
		user, scopes, err := ghapi.ValidatePAT(tok)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, 400
		}
		// write-only, 0600, jamás se re-muestra (ley de secretos, ADR-0010)
		if err := os.MkdirAll(ident.ConfigDir(), 0o700); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, 500
		}
		if err := os.WriteFile(tokenPath(), []byte(tok+"\n"), 0o600); err != nil {
			return map[string]any{"ok": false, "error": "no pude guardar el token: " + err.Error()}, 500
		}
		m.mu.Lock()
		m.st.GitHub = &GHState{Mode: "pat", User: user}
		m.logs.Append("github", "PAT validado — usuario "+user)
		m.setStepLocked("github", OK, "PAT · "+user, "")
		m.mu.Unlock()
		return map[string]any{"ok": true, "mode": "pat", "user": user, "scopes": scopes}, 200
	default:
		return map[string]any{"ok": false, "error": "mode debe ser gh | pat"}, 400
	}
}

// ghToken obtiene el token vigente según el modo configurado.
func (m *Manager) ghToken() (string, error) {
	m.mu.Lock()
	gh := m.st.GitHub
	m.mu.Unlock()
	if gh == nil {
		return "", errors.New("GitHub no configurado — completa el paso 2")
	}
	if gh.Mode == "gh" {
		tok, _, err := ghapi.Detect()
		return tok, err
	}
	b, err := os.ReadFile(tokenPath())
	if err != nil {
		return "", errors.New("el token guardado ya no está — reconecta GitHub")
	}
	return strings.TrimSpace(string(b)), nil
}

func (m *Manager) handleGithubOrgs(map[string]any) (any, int) {
	tok, err := m.ghToken()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 400
	}
	user, orgs, err := ghapi.ListOrgs(tok)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 502
	}
	return map[string]any{"ok": true, "user": user, "orgs": orgs}, 200
}

func (m *Manager) handleGithubRepos(body map[string]any) (any, int) {
	tok, err := m.ghToken()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 400
	}
	page := 1
	if p, ok := body["page"].(float64); ok && p >= 1 {
		page = int(p)
	}
	user := ""
	if m.st.GitHub != nil {
		user = m.st.GitHub.User
	}
	repos, err := ghapi.ListRepos(tok, user, str(body, "org"), page)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 502
	}
	return map[string]any{"ok": true, "repos": repos, "page": page, "has_more": len(repos) == 50}, 200
}

func (m *Manager) handleRepoTags(body map[string]any) (any, int) {
	tok, err := m.ghToken()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 400
	}
	tags, err := ghapi.ListTags(tok, str(body, "full_name"), 30)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, 400
	}
	return map[string]any{"ok": true, "tags": tags}, 200
}

// handleRepoSelect guarda la SELECCIÓN (no clona — eso es el paso clone).
// Valida cada nombre y ref: el navegador solo puede mandar lo que el server
// ya listó, y aun así se re-valida (anti-inyección).
func (m *Manager) handleRepoSelect(body map[string]any) (any, int) {
	raw, _ := body["repos"].([]any)
	if len(raw) == 0 {
		return map[string]any{"ok": false, "error": "selecciona al menos un repo"}, 400
	}
	sel := make([]RepoSel, 0, len(raw))
	seen := map[string]bool{}
	for _, it := range raw {
		mm, _ := it.(map[string]any)
		fn, ref := str(mm, "full_name"), str(mm, "ref")
		if !ghapi.ValidFullName(fn) {
			return map[string]any{"ok": false, "error": "nombre de repo inválido: " + fn}, 400
		}
		if !ghapi.ValidRef(ref) {
			return map[string]any{"ok": false, "error": "ref inválida: " + ref}, 400
		}
		base := filepath.Base(fn)
		if seen[base] {
			return map[string]any{"ok": false, "error": "dos repos con el mismo nombre base: " + base}, 400
		}
		seen[base] = true
		sel = append(sel, RepoSel{FullName: fn, Ref: ref, Status: Pending})
	}
	m.mu.Lock()
	// conserva el status de los ya clonados si la selección los repite
	old := map[string]RepoSel{}
	for _, r := range m.st.Repos {
		old[r.FullName] = r
	}
	for i, r := range sel {
		if o, ok := old[r.FullName]; ok && o.Ref == r.Ref && o.Status == OK {
			sel[i].Status = OK
		}
	}
	m.st.Repos = sel
	m.persistLocked()
	m.mu.Unlock()
	return map[string]any{"ok": true, "count": len(sel)}, 200
}
