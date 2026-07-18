package herdr

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Exec corre un comando arbitrario en un target SSH con el mismo blindaje que
// el resto del paquete: argv quoteado POSIX (jamás inyección), BatchMode,
// ControlMaster (una conexión autenticada reusada) y el PATH remoto aumentado.
//
// Contrato de streams — el que usa el plano de init remoto (ADR-0011 §4):
// stdout se devuelve entero (JSON de los subcomandos `--json`); stderr se
// entrega línea a línea a onLine (el progreso humano, va al LogBuffer).
// stdin opcional (answers por stdin, tokens por stdin — jamás en argv).
//
// HARNESS_SSH_BIN permite stubear ssh en tests (patrón HARNESS_GH_BIN).
func Exec(target string, argv []string, stdin []byte, timeout time.Duration, onLine func(string)) ([]byte, error) {
	if target == "" {
		return nil, fmt.Errorf("target vacío")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	remote := remotePATH + shJoin(argv)
	sshArgs := append(sshBase(), target, remote)
	cmd := exec.CommandContext(ctx, sshExecBin(), sshArgs...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var tail []string
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 128*1024), 128*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if onLine != nil {
			onLine(line)
		}
		tail = append(tail, line)
		if len(tail) > 5 {
			tail = tail[1:]
		}
	}
	if err := cmd.Wait(); err != nil {
		// el stderr trae la causa real (Permission denied, not found…)
		if len(tail) > 0 {
			return out.Bytes(), fmt.Errorf("%w: %s", err, strings.Join(tail, " · "))
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

func sshExecBin() string {
	if b := os.Getenv("HARNESS_SSH_BIN"); b != "" {
		return b
	}
	return "ssh"
}
