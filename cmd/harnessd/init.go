package main

import (
	"fmt"
	"strconv"

	"github.com/andresgarcia29/harness-daemon/internal/config"
	"github.com/andresgarcia29/harness-daemon/internal/lock"
)

// initCmd — `harness init`: la puerta de entrada al wizard de onboarding.
// Asegura un daemon (en modo setup si no hay ninguno) en el puerto del panel
// y abre el navegador en el wizard. Repetible: si ya hay daemon, solo abre;
// si hay un init a medias, el wizard reanuda solo (el estado es del server).
func initCmd(flagPort int, noOpen bool) int {
	port := config.ResolveUIPort(flagPort)
	url := fmt.Sprintf("http://127.0.0.1:%d/#/init", port)

	if h, err := lock.Probe(port); err == nil && h.Name == "harnessd" {
		fmt.Printf("✅ harnessd ya corriendo (pid %d, v%s, modo %s)\n", h.PID, h.Version, h.Mode)
		if h.Mode != "setup" {
			fmt.Println("   ese daemon ya observa un workspace; el wizard solo aparecerá si hay un init a medias ahí.")
		}
	} else {
		if rc := launchDetached(port, []string{"run", "--setup", "--port", strconv.Itoa(port)}); rc != 0 {
			return rc
		}
	}
	if noOpen {
		fmt.Printf("   wizard: %s\n", url)
		return 0
	}
	openBrowser(url)
	return 0
}
