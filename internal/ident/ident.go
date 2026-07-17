// Package ident resuelve las dos claves de identidad que NO se pueden cambiar
// después sin invalidar los datos históricos de todas las máquinas: quién es
// esta máquina, y qué workspace es este.
//
// Ver docs/adr/ADR-0002-modelo-de-identidad.md
package ident

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigDir es donde ya vive el token de Vault del harness. Misma carpeta,
// misma ley: config del usuario, chmod 600, fuera del repo.
func ConfigDir() string {
	if d := os.Getenv("HARNESS_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".harness"
	}
	return filepath.Join(home, ".config", "harness")
}

// MachineID devuelve un UUID estable por máquina, creándolo al primer uso.
//
// NO usamos el hostname a propósito: colisiona entre clientes (media industria
// tiene un "macbook-pro") y cambia en cuanto alguien renombra su laptop. Un id
// que muta parte el histórico en dos máquinas fantasma.
func MachineID() (string, error) {
	path := filepath.Join(ConfigDir(), "machine-id")
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generando machine-id: %w", err)
	}
	id := hex.EncodeToString(buf)
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return "", err
	}
	// 0600: es un identificador, no un secreto, pero vive junto a secretos.
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// Machine describe esta máquina para la tabla machines.
type Machine struct {
	ID       string
	Hostname string
	OS       string
	Arch     string
	Kind     string // laptop | vpc | k8s-cronjob | ci
}

func ThisMachine() (Machine, error) {
	id, err := MachineID()
	if err != nil {
		return Machine{}, err
	}
	host, _ := os.Hostname()
	return Machine{
		ID: id, Hostname: host,
		OS: runtime.GOOS, Arch: runtime.GOARCH,
		Kind: detectKind(),
	}, nil
}

func detectKind() string {
	// Un pod de K8s siempre trae esto; un CronJob del harness también.
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return "k8s-cronjob"
	}
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		return "ci"
	}
	if runtime.GOOS == "darwin" {
		return "laptop"
	}
	return "vpc"
}

// Workspace identifica una instancia del harness.
type Workspace struct {
	ID     string // hash del remote normalizado, o local:<machine>:<ruta>
	Remote string // github.com/corvux/atlas — vacío si no hay remote
	Name   string
	Path   string // hecho LOCAL de esta máquina, no la identidad
	Local  bool   // true = no se puede unificar entre máquinas
}

// ResolveWorkspace calcula la identidad de un workspace a partir de su git
// remote, NUNCA de su ruta.
//
// El mismo repo es /Users/andres/x en el Mac y /home/andres/x en el VPC. Si la
// clave fuera la ruta serían dos workspaces distintos y centralizar no
// centralizaría nada — que es justo lo que queremos evitar.
func ResolveWorkspace(path string, machineID string) (Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Workspace{}, err
	}
	ws := Workspace{Path: abs, Name: filepath.Base(abs)}

	remote := gitRemote(abs)
	if remote == "" {
		// Honesto: sin remote, este workspace ES local a esta máquina.
		// Fingir un id compartido mezclaría repos distintos con el mismo nombre.
		ws.Local = true
		ws.ID = "local:" + machineID + ":" + hash(abs)[:12]
		return ws, nil
	}
	ws.Remote = NormalizeRemote(remote)
	ws.ID = hash(ws.Remote)[:16]
	if i := strings.LastIndex(ws.Remote, "/"); i >= 0 {
		ws.Name = ws.Remote[i+1:]
	}
	return ws, nil
}

// NormalizeRemote lleva cualquier forma de URL de git a una clave estable.
//
//	git@github.com:corvux/atlas.git      → github.com/corvux/atlas
//	https://github.com/corvux/atlas.git  → github.com/corvux/atlas
//	ssh://git@github.com/corvux/atlas    → github.com/corvux/atlas
//
// Las tres son EL MISMO repo: si no las unificamos, un dev que clona por SSH y
// otro por HTTPS aparecen como dos workspaces y el tablero miente.
func NormalizeRemote(r string) string {
	r = strings.TrimSpace(r)
	r = strings.TrimSuffix(r, ".git")
	r = strings.TrimSuffix(r, "/")
	switch {
	case strings.HasPrefix(r, "git@"):
		r = strings.TrimPrefix(r, "git@")
		r = strings.Replace(r, ":", "/", 1)
	case strings.Contains(r, "://"):
		if i := strings.Index(r, "://"); i >= 0 {
			r = r[i+3:]
		}
		if i := strings.Index(r, "@"); i >= 0 { // user:pass@host — fuera credenciales
			r = r[i+1:]
		}
	}
	return strings.ToLower(r)
}

func gitRemote(dir string) string {
	cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
