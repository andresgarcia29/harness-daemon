package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/andresgarcia29/harness-daemon/internal/ident"
)

// askpassCmd — el GIT_ASKPASS del clone del wizard. Git nos invoca con el
// prompt como argumento; contestamos username fijo y el token según la fuente
// (PAT guardado 0600 o el token vivo de gh). El token JAMÁS pasa por argv de
// git ni por logs: viaja solo por este stdout que git consume.
//
// Oculto del usage a propósito: no es para humanos. main() nos enruta aquí
// también vía HARNESS_ASKPASS=1 (GIT_ASKPASS no acepta argumentos extra).
func askpassCmd(args []string) int {
	prompt := strings.ToLower(strings.Join(args, " "))
	if strings.Contains(prompt, "username") {
		fmt.Println("x-access-token")
		return 0
	}
	switch os.Getenv("HARNESS_ASKPASS_SOURCE") {
	case "ssh-env":
		// instalación de llave SSH (api.InstallSSHKey): la contraseña viaja
		// por env del proceso ssh, se usa una vez y se descarta
		pw := os.Getenv("HARNESS_SSH_PASSWORD")
		if pw == "" {
			return 1
		}
		fmt.Println(pw)
	case "gh":
		bin := os.Getenv("HARNESS_GH_BIN")
		if bin == "" {
			bin = "gh"
		}
		out, err := exec.Command(bin, "auth", "token").Output()
		if err != nil {
			return 1
		}
		fmt.Println(strings.TrimSpace(string(out)))
	default: // pat
		b, err := os.ReadFile(filepath.Join(ident.ConfigDir(), "github-token"))
		if err != nil {
			return 1
		}
		fmt.Println(strings.TrimSpace(string(b)))
	}
	return 0
}
