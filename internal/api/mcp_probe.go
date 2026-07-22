package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/redact"
)

// Sonda MCP: la ÚNICA prueba honesta de "funciona" es lanzar el servidor y
// hablarle su protocolo (JSON-RPC initialize + tools/list). herdr sólo hace
// checks estáticos; esto confirma que arranca, contesta, y qué tools expone.
// Cachea el último resultado por servidor para que el snapshot lo re-adjunte.

var (
	mcpProbeMu    sync.Mutex
	mcpProbeCache = map[string]McpProbe{}
)

func mcpProbeGet(name string) *McpProbe {
	mcpProbeMu.Lock()
	defer mcpProbeMu.Unlock()
	if p, ok := mcpProbeCache[name]; ok {
		return &p
	}
	return nil
}

func mcpProbeSet(name string, p McpProbe) {
	mcpProbeMu.Lock()
	mcpProbeCache[name] = p
	mcpProbeMu.Unlock()
}

const (
	mcpInit        = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"harness-daemon","version":"1"}}}` + "\n"
	mcpInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	mcpToolsList   = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
)

type McpConf struct {
	Command string
	Args    []string
	Env     map[string]string
}

func readMcpConfig(ws string) map[string]McpConf {
	out := map[string]McpConf{}
	b, err := os.ReadFile(filepath.Join(ws, ".mcp.json"))
	if err != nil {
		return out
	}
	var cfg struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return out
	}
	for n, sv := range cfg.Servers {
		out[n] = McpConf{Command: sv.Command, Args: sv.Args, Env: sv.Env}
	}
	return out
}

// ProbeAllMcp sondea TODOS los MCP de <ws>/.mcp.json en paralelo (cap 4) y
// cachea cada resultado (el snapshot lo re-adjunta con su timestamp). La usan
// el botón del panel Y la sonda automática periódica del daemon: el estado de
// los MCP se VE siempre, no solo cuando alguien se acuerda de apretar.
func ProbeAllMcp(ws string) map[string]McpProbe {
	servers := readMcpConfig(ws)
	out := map[string]McpProbe{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for name, sv := range servers {
		wg.Add(1)
		go func(name string, sv McpConf) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p := probeMcpServer(ws, sv)
			mcpProbeSet(name, p)
			mu.Lock()
			out[name] = p
			mu.Unlock()
		}(name, sv)
	}
	wg.Wait()
	return out
}

// OpProbeMcp: la sonda a demanda desde el panel.
func (o *Op) OpProbeMcp(rw http.ResponseWriter, r *http.Request) {
	if _, ok := o.Guard(rw, r); !ok {
		return
	}
	out := ProbeAllMcp(o.WS)
	o.emit("decision", "el humano sondeó los MCP desde el panel", "")
	writeJSON(rw, 200, map[string]any{"ok": true, "probed": out})
}

// probeMcpServer arranca un servidor MCP y le habla el protocolo. Docker o
// binario da igual: ambos hablan JSON-RPC por stdio.
// ProbeMcpCommand — sonda directa para el plano de init: valida un MCP (con
// un secreto inyectado por env si aplica) ANTES de persistir nada. Mismo
// handshake JSON-RPC honesto que OpProbeMcp.
func ProbeMcpCommand(ws, command string, args []string, env map[string]string) McpProbe {
	return probeMcpServer(ws, McpConf{Command: command, Args: args, Env: env})
}

func probeMcpServer(ws string, sv McpConf) McpProbe {
	start := time.Now()
	p := McpProbe{At: time.Now().UTC().Format(time.RFC3339)}
	cmd := sv.Command
	if !filepath.IsAbs(cmd) && strings.Contains(cmd, "/") {
		cmd = filepath.Join(ws, cmd) // with-secrets.sh y relativos, respecto al ws
	}
	// transporte ausente = error CLARO, no un timeout críptico
	if !strings.Contains(cmd, "/") {
		if _, err := exec.LookPath(cmd); err != nil {
			p.Error = "no encuentro «" + cmd + "» en PATH — el transporte de este MCP; instálalo (o elige la variante local)"
			p.Ms = ms(start)
			return p
		}
	}
	// docker puede estar BAJANDO la imagen la primera vez: más aire
	timeout := 12 * time.Second
	if filepath.Base(cmd) == "docker" {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, cmd, sv.Args...)
	c.Dir = ws
	c.Env = os.Environ()
	for k, v := range sv.Env {
		c.Env = append(c.Env, k+"="+v)
	}
	stdin, e1 := c.StdinPipe()
	stdout, e2 := c.StdoutPipe()
	var errbuf strings.Builder
	c.Stderr = &errbuf
	if e1 != nil || e2 != nil {
		p.Error, p.Ms = "no pude abrir los pipes", ms(start)
		return p
	}
	if err := c.Start(); err != nil {
		p.Error, p.Ms = redact.String(redact.Clip(err.Error(), 160)), ms(start)
		return p
	}
	// stdin se queda ABIERTO hasta tener las respuestas: hay servers (el de
	// GitHub) que tratan el EOF de stdin como shutdown y mueren sin contestar
	// si lo cierras al escribir — el bug clásico del probe impaciente.
	go func() {
		_, _ = io.WriteString(stdin, mcpInit)
		_, _ = io.WriteString(stdin, mcpInitialized)
		_, _ = io.WriteString(stdin, mcpToolsList)
	}()
	done := make(chan struct{})
	gotAll := make(chan struct{})
	var linesMu sync.Mutex
	var lines []string
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		answered := false
		for sc.Scan() {
			line := sc.Text()
			linesMu.Lock()
			lines = append(lines, line)
			linesMu.Unlock()
			// la respuesta a tools/list (id 2) = ya tenemos todo
			if !answered && (strings.Contains(line, `"id":2`) || strings.Contains(line, `"id": 2`)) {
				answered = true
				close(gotAll)
			}
		}
		close(done)
	}()
	select {
	case <-gotAll:
		// respuestas completas: ahora sí, adiós (stdin cerrado + kill)
		_ = stdin.Close()
		_ = c.Process.Kill()
		<-done
	case <-done:
	case <-ctx.Done():
		_ = stdin.Close()
		_ = c.Process.Kill()
		<-done // el proceso muere → stdout EOF → el lector termina
	}
	_ = c.Wait()
	p.Ms = ms(start)
	parseMcpResponses(&p, lines)
	if !p.OK {
		se := strings.ToLower(errbuf.String())
		if ctx.Err() != nil && p.Error == "" {
			p.Error = fmt.Sprintf("no contestó en %s (npx/uvx/docker bajan cosas la 1ª vez — reintenta en un momento)", timeout)
		}
		if p.Error == "" {
			tail := errbuf.String()
			if len(tail) > 240 {
				tail = tail[len(tail)-240:]
			}
			p.Error = redact.String(strings.TrimSpace(tail))
			if p.Error == "" {
				p.Error = "arrancó pero no contestó el initialize"
			}
		}
		for _, kw := range []string{"auth", "401", "403", "token", "credential", "apikey", "api key", "unauthor", "permission denied"} {
			if strings.Contains(se, kw) {
				p.AuthHint = true
				break
			}
		}
	}
	return p
}

func parseMcpResponses(p *McpProbe, lines []string) {
	for _, ln := range lines {
		var rec struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(ln), &rec) != nil {
			continue
		}
		switch strings.Trim(string(rec.ID), `"`) {
		case "1":
			if len(rec.Error) > 0 {
				p.Error = "el servidor rechazó initialize"
				continue
			}
			var res struct {
				ServerInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"serverInfo"`
			}
			_ = json.Unmarshal(rec.Result, &res)
			p.OK = true
			p.Server, p.Version = res.ServerInfo.Name, res.ServerInfo.Version
		case "2":
			if len(rec.Result) == 0 {
				continue
			}
			var res struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			_ = json.Unmarshal(rec.Result, &res)
			for _, t := range res.Tools {
				if t.Name != "" {
					p.Tools = append(p.Tools, t.Name)
				}
			}
		}
	}
}

func ms(t time.Time) int64 { return time.Since(t).Milliseconds() }
