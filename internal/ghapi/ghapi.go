// Package ghapi habla con GitHub para el wizard de init: detectar el gh CLI
// autenticado, validar un PAT, y listar orgs/repos/tags. Todo por REST con un
// token — gh solo se usa para OBTENER su token (`gh auth token`), así el resto
// del código tiene un único camino.
//
// El token jamás aparece en argv ni logs; viaja solo en el header Authorization.
package ghapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Overridables para tests (httptest + stub de gh).
var APIBase = "https://api.github.com"

func ghBin() string {
	if b := os.Getenv("HARNESS_GH_BIN"); b != "" {
		return b
	}
	return "gh"
}

var httpc = &http.Client{Timeout: 12 * time.Second}

type User struct {
	Login string `json:"login"`
}
type Org struct {
	Login string `json:"login"`
}
type Repo struct {
	FullName      string `json:"full_name"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Description   string `json:"description"`
	Language      string `json:"language"`
	PushedAt      string `json:"pushed_at"`
}

// Detect: ¿hay un gh CLI ya autenticado? Devuelve su token y el usuario.
func Detect() (token, user string, err error) {
	path, err := exec.LookPath(ghBin())
	if err != nil {
		return "", "", fmt.Errorf("gh no está en PATH")
	}
	out, err := exec.Command(path, "auth", "token").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return "", "", fmt.Errorf("gh está instalado pero no autenticado (corre `gh auth login`)")
	}
	token = strings.TrimSpace(string(out))
	u, err := whoami(token)
	if err != nil {
		return "", "", fmt.Errorf("el token de gh no valida contra la API: %w", err)
	}
	return token, u, nil
}

// ValidatePAT valida un token pegado contra /user y devuelve login + scopes.
func ValidatePAT(token string) (user string, scopes []string, err error) {
	req, _ := http.NewRequest("GET", APIBase+"/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpc.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("GitHub contestó %d — ¿token vigente?", resp.StatusCode)
	}
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", nil, err
	}
	for _, s := range strings.Split(resp.Header.Get("X-OAuth-Scopes"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			scopes = append(scopes, s)
		}
	}
	return u.Login, scopes, nil
}

func whoami(token string) (string, error) {
	u, _, err := ValidatePAT(token)
	return u, err
}

func get(token, path string, out any) error {
	req, _ := http.NewRequest("GET", APIBase+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub contestó %d en %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ListOrgs: las organizaciones del usuario + su cuenta personal (primera).
func ListOrgs(token string) (user string, orgs []string, err error) {
	user, err = whoami(token)
	if err != nil {
		return "", nil, err
	}
	var os []Org
	if err := get(token, "/user/orgs?per_page=100", &os); err != nil {
		return "", nil, err
	}
	orgs = []string{user}
	for _, o := range os {
		orgs = append(orgs, o.Login)
	}
	return user, orgs, nil
}

// ListRepos: los repos de una org (o de la cuenta personal), paginados,
// ordenados por push reciente — lo que quieres clonar suele estar arriba.
func ListRepos(token, user, org string, page int) ([]Repo, error) {
	if page < 1 {
		page = 1
	}
	path := fmt.Sprintf("/orgs/%s/repos?per_page=50&sort=pushed&page=%d", org, page)
	if org == user {
		path = fmt.Sprintf("/user/repos?affiliation=owner&per_page=50&sort=pushed&page=%d", page)
	}
	var rs []Repo
	if err := get(token, path, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// ListTags: los tags de un repo (para clonar pineado, opcional).
func ListTags(token, fullName string, limit int) ([]string, error) {
	if !ValidFullName(fullName) {
		return nil, fmt.Errorf("nombre de repo inválido")
	}
	var ts []struct {
		Name string `json:"name"`
	}
	if err := get(token, fmt.Sprintf("/repos/%s/tags?per_page=%d", fullName, limit), &ts); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out, nil
}

var fullNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ValidFullName: owner/name sin rarezas — lo único que el navegador puede
// mandar sobre un repo, y aun así validado (anti-inyección).
func ValidFullName(fn string) bool {
	return fullNameRe.MatchString(fn) && !strings.HasPrefix(fn, "-") && !strings.Contains(fn, "..")
}

// ValidRef: tag o branch razonable (sin flags ni traversal).
var refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]*$`)

func ValidRef(ref string) bool {
	return ref == "" || (refRe.MatchString(ref) && !strings.Contains(ref, ".."))
}
