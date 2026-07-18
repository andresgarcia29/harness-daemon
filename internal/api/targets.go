package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/andresgarcia29/harness-daemon/internal/ident"
)

// Target es una máquina remota (VPS) donde corre herdr, alcanzable por SSH. El
// panel local la controla vía `ssh <SSH> herdr …`. SSH es un alias de
// ~/.ssh/config o user@host — la AUTENTICACIÓN la maneja OpenSSH (llaves), el
// daemon nunca toca credenciales. El frontend manda el NAME; el daemon lo
// resuelve al SSH validado — así el navegador nunca inyecta un target de exec.
type Target struct {
	Name string `json:"name"`
	SSH  string `json:"ssh"`
	// Path del workspace del harness EN el VPS (para `harnessd snapshot
	// --workspace <path>`). El workspaceID es el mismo en ambas máquinas (se
	// deriva del git remote), sólo cambia la ruta. Opcional.
	Path string `json:"path,omitempty"`
}

var targetsMu sync.Mutex

func targetsPath() string { return filepath.Join(ident.ConfigDir(), "targets.json") }

// nombres y ssh saneados: el ssh se pasa como argv a `ssh` (sin shell), pero un
// target que empiece con '-' inyectaría OPCIONES de ssh (p.ej. -oProxyCommand).
// Por eso lo restringimos a un charset seguro y prohibimos el guion inicial.
var (
	reTargetName = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,40}$`)
	reTargetSSH  = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,120}$`)
)

func validTargetName(n string) bool { return reTargetName.MatchString(n) }
func validTargetSSH(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && reTargetSSH.MatchString(s)
}

// validTargetPath: ruta del workspace remoto. Vacía = ok (usa el default del
// harnessd remoto). Si se da, debe ser absoluta o ~, sin ".." ni guion inicial
// (va quoteada al ssh, pero validamos para no meter flags a harnessd).
func validTargetPath(p string) bool {
	if p == "" {
		return true
	}
	if strings.HasPrefix(p, "-") || strings.Contains(p, "..") {
		return false
	}
	return (strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~")) && !strings.ContainsAny(p, "\n\r")
}

// LoadTargets lee la lista (vacía si no hay archivo). Nunca falla ruidoso.
func LoadTargets() []Target {
	targetsMu.Lock()
	defer targetsMu.Unlock()
	return loadTargetsLocked()
}

func loadTargetsLocked() []Target {
	b, err := os.ReadFile(targetsPath())
	if err != nil {
		return []Target{}
	}
	var ts []Target
	if json.Unmarshal(b, &ts) != nil {
		return []Target{}
	}
	if ts == nil {
		ts = []Target{}
	}
	return ts
}

func saveTargetsLocked(ts []Target) error {
	b, _ := json.MarshalIndent(ts, "", "  ")
	dir := filepath.Dir(targetsPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Escritura ATÓMICA: tmp + rename. Un crash a mitad no deja el archivo
	// truncado (que loadTargets leería como "cero targets" y perderías todo).
	tmp := targetsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, targetsPath())
}

// AddTarget agrega/actualiza un target (por nombre). Valida nombre, ssh y path.
func AddTarget(name, ssh, path string) error {
	name = strings.TrimSpace(name)
	ssh = strings.TrimSpace(ssh)
	path = strings.TrimSpace(path)
	if !validTargetName(name) {
		return fmt.Errorf("nombre inválido (letras, números, espacio, . _ - ; máx 40)")
	}
	if !validTargetSSH(ssh) {
		return fmt.Errorf("destino SSH inválido (usa un alias de ~/.ssh/config o user@host, sin espacios)")
	}
	if !validTargetPath(path) {
		return fmt.Errorf("ruta del workspace inválida (debe ser absoluta o ~, sin ..)")
	}
	targetsMu.Lock()
	defer targetsMu.Unlock()
	ts := loadTargetsLocked()
	found := false
	for i := range ts {
		if ts[i].Name == name {
			ts[i].SSH, ts[i].Path, found = ssh, path, true
		}
	}
	if !found {
		ts = append(ts, Target{Name: name, SSH: ssh, Path: path})
	}
	return saveTargetsLocked(ts)
}

// RemoveTarget borra un target por nombre.
func RemoveTarget(name string) error {
	targetsMu.Lock()
	defer targetsMu.Unlock()
	ts := loadTargetsLocked()
	out := ts[:0]
	for _, t := range ts {
		if t.Name != name {
			out = append(out, t)
		}
	}
	return saveTargetsLocked(out)
}

// ResolveTarget traduce un NOMBRE de target al string SSH validado. name vacío o
// "local" → ("", true) = esta máquina. Un nombre desconocido → ("", false): el
// caller DEBE rechazar (así el navegador no puede pedir un exec a un destino
// arbitrario — sólo a los que el humano agregó por el plano de operar).
func ResolveTarget(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == "local" {
		return "", true
	}
	for _, t := range LoadTargets() {
		if t.Name == name {
			return t.SSH, true
		}
	}
	return "", false
}

// ResolveTargetFull devuelve el Target completo (ssh + path) por nombre. name
// vacío/"local" → (zero, true) = esta máquina. Desconocido → (zero, false).
func ResolveTargetFull(name string) (Target, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == "local" {
		return Target{}, true
	}
	for _, t := range LoadTargets() {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}
