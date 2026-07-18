package api

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// InstallSSHKey instala TU llave pública en el authorized_keys de un VPS que
// aún pide contraseña — el paso que faltaba para dar de alta una máquina
// virgen sin salir del panel. La contraseña se usa UNA vez, viaja por env del
// proceso ssh (jamás argv, jamás disco) y se descarta; a partir de ahí todo
// es llave, como siempre (BatchMode).
//
// Mecánica: ssh sin TTY (Setsid) con SSH_ASKPASS apuntando a NUESTRO binario
// (modo HARNESS_ASKPASS=1, fuente ssh-env) — el truco estándar para password
// no-interactivo sin depender de sshpass.
func InstallSSHKey(dest, password string) error {
	if !validTargetSSH(dest) {
		return fmt.Errorf("destino SSH inválido")
	}
	if password == "" {
		return fmt.Errorf("contraseña vacía")
	}
	pub, err := ensureLocalKey()
	if err != nil {
		return err
	}
	selfExe, err := os.Executable()
	if err != nil {
		return err
	}
	// el pubkey es base64+espacios (sin comillas simples): seguro entre ''
	remote := "umask 077; mkdir -p \"$HOME/.ssh\"; touch \"$HOME/.ssh/authorized_keys\"; " +
		"grep -qF '" + pub + "' \"$HOME/.ssh/authorized_keys\" || echo '" + pub + "' >> \"$HOME/.ssh/authorized_keys\""
	args := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-o", "NumberOfPasswordPrompts=1",
		"-o", "PreferredAuthentications=password,keyboard-interactive",
		dest, remote,
	}
	cmd := exec.Command("ssh", args...)
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+selfExe,
		"SSH_ASKPASS_REQUIRE=force", // OpenSSH ≥8.4; el Setsid cubre a los viejos
		"DISPLAY=:0",                // los ssh viejos exigen DISPLAY para usar ASKPASS
		"HARNESS_ASKPASS=1",
		"HARNESS_ASKPASS_SOURCE=ssh-env",
		"HARNESS_SSH_PASSWORD="+password,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // sin TTY: fuerza el ASKPASS
	cmd.Stdin = nil
	done := make(chan error, 1)
	var out []byte
	go func() {
		var err error
		out, err = cmd.CombinedOutput()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if strings.Contains(msg, "Permission denied") {
				return fmt.Errorf("el VPS rechazó la contraseña")
			}
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("no pude instalar la llave: %s", redactLine(msg))
		}
		return nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return fmt.Errorf("timeout hablando con el VPS")
	}
}

func redactLine(s string) string {
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// ensureLocalKey devuelve tu llave pública, generando una ed25519 (sin
// passphrase, la práctica estándar para automatización) si no tienes ninguna.
func ensureLocalKey() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sshDir := filepath.Join(home, ".ssh")
	for _, name := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
		if b, err := os.ReadFile(filepath.Join(sshDir, name)); err == nil {
			if k := strings.TrimSpace(string(b)); k != "" && !strings.Contains(k, "'") {
				return k, nil
			}
		}
	}
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return "", err
	}
	keyPath := filepath.Join(sshDir, "id_ed25519")
	out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath, "-C", "harness").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("no tienes llave SSH y no pude generar una: %s", strings.TrimSpace(string(out)))
	}
	b, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
