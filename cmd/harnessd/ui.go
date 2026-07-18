package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/andresgarcia29/harness-daemon/internal/config"
)

// uiCmd — `harness ui`: asegura el daemon en el puerto del panel (flag >
// config.json > 7180) y abre el navegador. Es `ensure` con azúcar: la puerta
// de entrada humana al panel sin recordar puertos.
func uiCmd(flagPort int, ws string, noOpen bool) int {
	port := config.ResolveUIPort(flagPort)
	if rc := ensure(port, ws); rc != 0 {
		return rc
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	if noOpen {
		fmt.Printf("   panel: %s\n", url)
		return 0
	}
	openBrowser(url)
	return 0
}

// openBrowser abre la URL con el abridor del sistema. Fail-open: si no hay
// navegador (VPS headless), imprime la URL y ya — jamás es un error.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		if _, err := exec.LookPath("xdg-open"); err != nil {
			fmt.Printf("   panel: %s\n", url)
			return
		}
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("   panel: %s\n", url)
		return
	}
	_ = cmd.Process.Release()
	fmt.Printf("   panel: %s (abierto en el navegador)\n", url)
}

// configCmd — `harness config` muestra; `harness config set ui_port N` escribe.
func configCmd(args []string) int {
	if len(args) == 0 {
		c := config.Load()
		fmt.Printf("archivo   %s\n", config.Path())
		fmt.Printf("ui_port   %d", config.ResolveUIPort(0))
		if c.UIPort == 0 {
			fmt.Printf(" (default)")
		}
		fmt.Println()
		return 0
	}
	if len(args) == 3 && args[0] == "set" && args[1] == "ui_port" {
		var p int
		if _, err := fmt.Sscanf(args[2], "%d", &p); err != nil || p < 1 || p > 65535 {
			fmt.Fprintf(os.Stderr, "❌ puerto inválido: %q\n", args[2])
			return 1
		}
		c := config.Load()
		c.UIPort = p
		if err := config.Save(c); err != nil {
			fmt.Fprintf(os.Stderr, "❌ no pude guardar: %v\n", err)
			return 1
		}
		fmt.Printf("✅ ui_port = %d → %s\n", p, config.Path())
		return 0
	}
	fmt.Fprint(os.Stderr, "uso: harness config | harness config set ui_port <puerto>\n")
	return 2
}
