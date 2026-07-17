// harnessd — el daemon del harness. Observa, no ejecuta.
//
// Modos (ADR-0001): all-in-one · collect · serve
// Leyes: solo 127.0.0.1 · nunca ejecuta código del repo · nunca actúa sobre el
// workspace · si no cabe en el tablero, no sale de la máquina (ADR-0007/0009).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/api"
	"github.com/andresgarcia29/harness-daemon/internal/collect"
	"github.com/andresgarcia29/harness-daemon/internal/ident"
	"github.com/andresgarcia29/harness-daemon/internal/lock"
	"github.com/andresgarcia29/harness-daemon/internal/store"
	"github.com/andresgarcia29/harness-daemon/internal/webui"
)

// Version la inyecta el build: -ldflags "-X main.Version=0.1.0"
var Version = "dev"

const defaultPort = 7718

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	port := fs.Int("port", defaultPort, "puerto (solo 127.0.0.1)")
	ws := fs.String("workspace", ".", "workspace a observar")
	_ = fs.Parse(os.Args[2:])

	switch cmd {
	case "version":
		fmt.Println(Version)
	case "selftest":
		os.Exit(selftest())
	case "ensure":
		os.Exit(ensure(*port, *ws))
	case "run":
		os.Exit(run(*port, *ws))
	case "status":
		os.Exit(status(*port))
	case "stop":
		os.Exit(stop(*port))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `harnessd — observa el trabajo de los agentes. Solo lectura, solo local.

  harnessd ensure     arranca si no hay ninguno (idempotente — diez sesiones,
                      un daemon: gana quien logre el bind del puerto)
  harnessd run        arranca en primer plano
  harnessd status     pregunta quién tiene el puerto
  harnessd stop       lo para
  harnessd version    versión
  harnessd selftest   verificación de arranque (la usa el updater ANTES de
                      cambiar el binario: si esto falla, no hay swap)

Flags: --port (7718) --workspace (.)
`)
}

// selftest: ¿este binario arranca en esta máquina? Lo corre el updater sobre el
// binario NUEVO antes de hacer el rename. Atrapa arquitectura equivocada,
// descarga corrupta y glibc vieja — antes de que sea el binario en producción.
// Ver ADR-0006.
func selftest() int {
	m, err := ident.ThisMachine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ selftest: identidad de máquina: %v\n", err)
		return 1
	}
	tmp, err := os.MkdirTemp("", "harnessd-selftest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ selftest: no puedo escribir en temp: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "probe"), []byte("ok"), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "❌ selftest: escritura: %v\n", err)
		return 1
	}
	fmt.Printf("✅ harnessd %s · %s/%s · máquina %s… · ok\n",
		Version, m.OS, m.Arch, m.ID[:8])
	return 0
}

// ensure: el que llaman las diez sesiones. Idempotente por diseño — y SIEMPRE
// regresa: si no hay daemon, se relanza a sí mismo con `run` en segundo plano
// (sesión nueva, logs en ConfigDir) y espera a que /health conteste.
//
// La primera versión llamaba run() en primer plano: `make init` se quedaba
// colgado para siempre con la terminal secuestrada. "El auto arranca" significa
// que ensure LANZA y SE VA, no que ensure SE CONVIERTE en el daemon.
func ensure(port int, wsPath string) int {
	if h, err := lock.Probe(port); err == nil && h.Name == "harnessd" {
		fmt.Printf("✅ harnessd ya corriendo (pid %d, v%s) → http://127.0.0.1:%d\n", h.PID, h.Version, port)
		return 0 // nueve de cada diez llegan aquí, en ~5ms
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ no sé quién soy: %v\n", err)
		return 1
	}
	absWS, _ := filepath.Abs(wsPath)
	logPath := filepath.Join(ident.ConfigDir(), "harnessd.log")
	_ = os.MkdirAll(ident.ConfigDir(), 0o700)
	logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ log: %v\n", err)
		return 1
	}
	defer logf.Close()
	cmd := exec.Command(exe, "run", "--port", strconv.Itoa(port), "--workspace", absWS)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // sobrevive al cierre de la terminal
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ no pude lanzar: %v\n", err)
		return 1
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	// esperar a que conteste — o decir POR QUÉ no (el log tiene la razón)
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if h, err := lock.Probe(port); err == nil && h.Name == "harnessd" {
			fmt.Printf("✅ harnessd arrancado (pid %d, v%s) → http://127.0.0.1:%d\n", h.PID, h.Version, port)
			fmt.Printf("   logs: %s\n", logPath)
			return 0
		}
	}
	fmt.Fprintf(os.Stderr, "❌ lancé pid %d pero /health no contestó en 3s — mira %s\n", pid, logPath)
	return 1
}

func run(port int, wsPath string) int {
	started := time.Now()
	m, err := ident.ThisMachine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	w, err := ident.ResolveWorkspace(wsPath, m.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}

	ln, err := lock.Acquire(port)
	if err != nil {
		// No es un fallo: es el singleton funcionando.
		fmt.Fprintf(os.Stderr, "ℹ️  %v\n", err)
		if err == lock.ErrPortBusyForeign {
			fmt.Fprintf(os.Stderr, "   ↳ el puerto %d lo tiene otro proceso. Usa --port o libéralo.\n", port)
			return 1
		}
		return 0
	}
	defer ln.Close()

	dbPath := filepath.Join(ident.ConfigDir(), "harness.db")
	health := lock.Health{
		Name: "harnessd", Version: Version, Protocol: lock.Protocol,
		PID: os.Getpid(), Mode: "all-in-one", Started: started.Unix(),
		DB: dbPath,
	}

	// Fase 3: el colector. El almacén abre (y migra) ANTES de anunciar salud:
	// un daemon que dice "ok" sin poder escribir es un daemon que miente.
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ almacén: %v\n", err)
		return 1
	}
	defer st.Close()
	_ = st.SeedBuiltinPrices() // no pisa ediciones del usuario; sin esto todo costo es NULL
	col := collect.New(st, m, w)
	collectQuit := make(chan struct{})
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-collectQuit:
				return
			case now := <-t.C:
				_ = col.Tick(now.Unix()) // fail-open: telemetría jamás tumba al daemon
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(health)
	})
	// /api/stats: la prueba de vida del colector es que estos números CRECEN.
	mux.HandleFunc("/api/stats", func(rw http.ResponseWriter, r *http.Request) {
		counts, err := st.Counts()
		rw.Header().Set("Content-Type", "application/json")
		if err != nil {
			rw.WriteHeader(500)
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": err.Error()})
			return
		}
		costs, _ := st.Costs()
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"rows": counts, "costs": costs, "workspace": w.Name, "db": dbPath,
		})
	})
	// Fase 1: el daemon ES el backend del panel. Sirve el snapshot (leído del
	// SQLite) y el build de React embebido. Un proceso, todos los workspaces.
	mux.HandleFunc("/api/state", func(rw http.ResponseWriter, r *http.Request) {
		snap, err := api.Build(st.DB, w.ID, w.Path, time.Now().Unix())
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Cache-Control", "no-store")
		if err != nil {
			rw.WriteHeader(500)
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(rw).Encode(snap)
	})
	// SSE: el frontend espera un evento "snapshot" con el estado entero. El
	// colector ya escribe cada 2 s; aquí re-emitimos el snapshot al mismo ritmo.
	mux.HandleFunc("/api/stream", func(rw http.ResponseWriter, r *http.Request) {
		fl, ok := rw.(http.Flusher)
		if !ok {
			http.Error(rw, "sin streaming", http.StatusInternalServerError)
			return
		}
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Header().Set("Cache-Control", "no-store")
		rw.Header().Set("Connection", "keep-alive")
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		send := func() bool {
			snap, err := api.Build(st.DB, w.ID, w.Path, time.Now().Unix())
			if err != nil {
				return true
			}
			b, _ := json.Marshal(snap)
			if _, err := fmt.Fprintf(rw, "event: snapshot\ndata: %s\n\n", b); err != nil {
				return false
			}
			fl.Flush()
			return true
		}
		if !send() {
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case <-t.C:
				if !send() {
					return
				}
			}
		}
	})
	// Paridad de lectura: el grafo completo de una tarea, sus chips de git.
	mux.HandleFunc("/api/task-events", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(api.TaskEvents(st.DB, w.ID, r.URL.Query().Get("task")))
	})
	mux.HandleFunc("/api/task-git", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(api.BuildTaskGit(w.Path, r.URL.Query().Get("task")))
	})
	mux.HandleFunc("/api/session", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		d, err := api.BuildSession(st.DB, r.URL.Query().Get("id"), time.Now().Unix())
		if err != nil {
			rw.WriteHeader(500)
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(rw).Encode(d)
	})

	// El build de React (embebido). Va AL FINAL: solo atrapa lo que no matchea
	// /api ni /health ni /admin.
	web := webui.Handler()
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) { web.ServeHTTP(rw, r) })

	quit := make(chan struct{})
	mux.HandleFunc("/admin/quit", func(rw http.ResponseWriter, r *http.Request) {
		// Solo 127.0.0.1 (el listener ya lo garantiza), y solo POST: un GET
		// desde una pestaña cualquiera no debe poder matar el daemon.
		if r.Method != http.MethodPost {
			http.Error(rw, "usa POST", http.StatusMethodNotAllowed)
			return
		}
		rw.WriteHeader(200)
		close(quit)
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	fmt.Printf("🔭 harnessd %s → http://127.0.0.1:%d\n", Version, port)
	fmt.Printf("   máquina   %s… (%s/%s, %s)\n", m.ID[:8], m.OS, m.Arch, m.Kind)
	if w.Local {
		fmt.Printf("   workspace %s — SIN git remote: es local a esta máquina y no se puede unificar con otras\n", w.Name)
	} else {
		fmt.Printf("   workspace %s → %s\n", w.Name, w.Remote)
	}
	fmt.Printf("   solo lectura · solo 127.0.0.1 · Ctrl-C para salir\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-quit:
	}
	close(collectQuit)
	fmt.Println("\n👋 harnessd fuera. No tocó nada.")
	return 0
}

func status(port int) int {
	h, err := lock.Probe(port)
	if err != nil {
		fmt.Printf("○ no hay harnessd en %d (%v)\n", port, err)
		return 1
	}
	fmt.Printf("● harnessd v%s · pid %d · modo %s · protocolo %d\n", h.Version, h.PID, h.Mode, h.Protocol)
	fmt.Printf("  arriba desde %s\n", time.Unix(h.Started, 0).Format(time.RFC3339))
	fmt.Printf("  db %s\n", h.DB)
	return 0
}

func stop(port int) int {
	h, err := lock.Probe(port)
	if err != nil {
		fmt.Printf("○ no había nada que parar en %d\n", port)
		return 0
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Post(fmt.Sprintf("http://127.0.0.1:%d/admin/quit", port), "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ no pude pararlo: %v\n", err)
		return 1
	}
	resp.Body.Close()
	// El daemon es GLOBAL y `make stop` es por workspace: si tienes tres repos
	// abiertos, acabas de parar el que usaban los otros dos. Se acepta a
	// propósito (ADR-0005) — pero hay que DECIRLO, no dejar que se pregunte
	// por qué su panel murió.
	fmt.Printf("👋 parado harnessd v%s (pid %d).\n", h.Version, h.PID)
	fmt.Printf("   Ojo: el daemon es global. Si tenías otros workspaces abiertos,\n")
	fmt.Printf("   también se quedaron sin panel. `harnessd ensure` lo revive.\n")
	return 0
}
