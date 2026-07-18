package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/andresgarcia29/harness-daemon/internal/ident"
)

// updateCmd — `harness update`: actualiza el binario y ofrece regenerar cada
// instancia registrada.
//
// Bajo brew (el camino canónico), el binario lo actualiza brew: aquí solo se
// dispara `brew upgrade` — un solo gestor de versiones, cero rutas mágicas.
// Fuera de brew se imprime la guía (el auto-update firmado del ADR-0006 llega
// con la infraestructura de firma; hasta entonces, honestidad > magia).
func updateCmd(args []string) int {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	yes := fs.Bool("yes", false, "regenerar instancias sin preguntar")
	_ = fs.Parse(args)

	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	underBrew := strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/homebrew/") || strings.Contains(exe, "/linuxbrew/")

	fmt.Printf("harness %s (%s)\n", Version, exe)
	if underBrew {
		fmt.Println("── actualizando vía brew ──")
		cmd := exec.Command("brew", "upgrade", "andresgarcia29/agm/harness")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ brew upgrade falló: %v\n", err)
			return 1
		}
	} else {
		fmt.Println("ℹ️  este binario no vive bajo brew. Actualízalo con:")
		fmt.Println("    brew install andresgarcia29/agm/harness")
		fmt.Println("  o baja el release de github.com/andresgarcia29/harness-daemon/releases")
	}

	// instancias registradas por el generador
	var list []string
	if b, err := os.ReadFile(filepath.Join(ident.ConfigDir(), "instances.json")); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	if len(list) == 0 {
		fmt.Println("   (sin instancias registradas — nada más que actualizar)")
		return 0
	}
	fmt.Printf("── %d instancia(s) registradas ──\n", len(list))
	self := exe
	for _, ws := range list {
		if _, err := os.Stat(filepath.Join(ws, "harness-answers.yaml")); err != nil {
			fmt.Printf("  ○ %s — sin answers (¿movida/borrada?), la salto\n", ws)
			continue
		}
		if !*yes {
			fmt.Printf("  ↻ %s — regenerar con: %s generate --workspace %s\n", ws, filepath.Base(self), ws)
			continue
		}
		fmt.Printf("  ↻ %s\n", ws)
		cmd := exec.Command(self, "generate", "--workspace", ws)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "    ❌ %v (sigo con las demás)\n", err)
		}
	}
	if !*yes {
		fmt.Println("   corre con --yes para regenerarlas ahora (lo personalizado queda en .new, jamás se pisa)")
	}
	return 0
}
