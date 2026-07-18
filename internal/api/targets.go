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

// AddTarget agrega/actualiza un target (por nombre). Valida nombre y ssh.
func AddTarget(name, ssh string) error {
	name = strings.TrimSpace(name)
	ssh = strings.TrimSpace(ssh)
	if !validTargetName(name) {
		return fmt.Errorf("nombre inválido (letras, números, espacio, . _ - ; máx 40)")
	}
	if !validTargetSSH(ssh) {
		return fmt.Errorf("destino SSH inválido (usa un alias de ~/.ssh/config o user@host, sin espacios)")
	}
	targetsMu.Lock()
	defer targetsMu.Unlock()
	ts := loadTargetsLocked()
	found := false
	for i := range ts {
		if ts[i].Name == name {
			ts[i].SSH, found = ssh, true
		}
	}
	if !found {
		ts = append(ts, Target{Name: name, SSH: ssh})
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
