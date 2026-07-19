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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/api"
	"github.com/andresgarcia29/harness-daemon/internal/collect"
	"github.com/andresgarcia29/harness-daemon/internal/config"
	"github.com/andresgarcia29/harness-daemon/internal/herdr"
	"github.com/andresgarcia29/harness-daemon/internal/ident"
	"github.com/andresgarcia29/harness-daemon/internal/initflow"
	"github.com/andresgarcia29/harness-daemon/internal/lock"
	"github.com/andresgarcia29/harness-daemon/internal/store"
	"github.com/andresgarcia29/harness-daemon/internal/webui"
)

// Version la inyecta el build: -ldflags "-X main.Version=0.1.0"
var Version = "dev"

const defaultPort = 7718

func main() {
	// Modo askpass por env: GIT_ASKPASS invoca el binario con el prompt como
	// único argumento (sin subcomando posible). Ver askpass.go.
	if os.Getenv("HARNESS_ASKPASS") == "1" {
		os.Exit(askpassCmd(os.Args[1:]))
	}
	args := os.Args[1:]
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	cmd := args[0]
	rest := args[1:]
	// `harness daemon <sub>` es el namespace explícito del set legacy
	// (run/ensure/status/stop/…); los mismos verbos siguen vivos top-level.
	if cmd == "daemon" {
		if len(rest) < 1 {
			usage()
			os.Exit(2)
		}
		cmd, rest = rest[0], rest[1:]
	}
	// generate/discover parsean sus propios flags (tienen set propio)
	switch cmd {
	case "generate":
		os.Exit(generateCmd(rest))
	case "discover":
		os.Exit(discoverCmd(rest))
	case "update":
		os.Exit(updateCmd(rest))
	case "init-step":
		os.Exit(initStepCmd(rest))
	case "ratify":
		os.Exit(ratifyCmd(rest))
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	// port=0 significa "no especificado": los comandos del daemon caen a 7718
	// (legacy) y los del panel resuelven por config.json → 7180 (ADR-0011).
	port := fs.Int("port", 0, "puerto (solo 127.0.0.1)")
	ws := fs.String("workspace", ".", "workspace a observar")
	noOpen := fs.Bool("no-open", false, "no abrir el navegador")
	setup := fs.Bool("setup", false, "arranca sin workspace: el wizard de init lo fija (ADR-0011)")
	_ = fs.Parse(rest)
	dport := *port
	if dport == 0 {
		dport = defaultPort
	}

	switch cmd {
	case "version":
		fmt.Println(Version)
	case "selftest":
		os.Exit(selftest())
	case "ensure":
		os.Exit(ensure(dport, *ws))
	case "run":
		os.Exit(run(dport, *ws, *setup))
	case "snapshot":
		os.Exit(snapshotCmd(*ws))
	case "status":
		os.Exit(status(findDaemonPort(*port)))
	case "stop":
		os.Exit(stop(findDaemonPort(*port)))
	case "ui":
		os.Exit(uiCmd(*port, *ws, *noOpen))
	case "init":
		os.Exit(initCmd(*port, *noOpen))
	case "config":
		os.Exit(configCmd(fs.Args()))
	case "askpass":
		os.Exit(askpassCmd(fs.Args()))
	default:
		usage()
		os.Exit(2)
	}
}

// findDaemonPort — dónde vive el daemon cuando no diste --port: primero el
// puerto del panel (config → 7180, donde lo dejan `harness ui`/`init`), luego
// el legacy 7718. Sin esto, `harness stop` apuntaba a 7718 mientras el wizard
// corría en 7180 — y "lo paré" no paraba nada.
func findDaemonPort(flagPort int) int {
	if flagPort > 0 {
		return flagPort
	}
	ui := config.ResolveUIPort(0)
	if h, err := lock.Probe(ui); err == nil && h.Name == "harnessd" {
		return ui
	}
	if h, err := lock.Probe(defaultPort); err == nil && h.Name == "harnessd" {
		return defaultPort
	}
	return ui
}

func usage() {
	fmt.Fprint(os.Stderr, `harness — el harness de ingeniería agéntica. Panel solo-local, solo 127.0.0.1.

Panel:
  harness ui          asegura el daemon y abre el panel (puerto: --port >
                      config.json > 7180)
  harness config      muestra la config · harness config set ui_port <n>

Daemon (también bajo el namespace «harness daemon <cmd>»):
  harness ensure      arranca si no hay ninguno (idempotente — diez sesiones,
                      un daemon: gana quien logre el bind del puerto)
  harness run         arranca en primer plano
  harness status      pregunta quién tiene el puerto
  harness stop        lo para
  harness version     versión
  harness selftest    verificación de arranque (la usa el updater ANTES de
                      cambiar el binario: si esto falla, no hay swap)
  harness snapshot    imprime el snapshot del workspace como JSON

Flags: --port (daemon: 7718 · panel: 7180) --workspace (.) --no-open
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
	absWS, _ := filepath.Abs(wsPath)
	return launchDetached(port, []string{"run", "--port", strconv.Itoa(port), "--workspace", absWS})
}

// launchDetached se relanza a sí mismo con `args` en sesión propia (sobrevive
// al cierre de la terminal), logs en ConfigDir, y espera a que /health conteste.
// Lo comparten ensure (daemon normal) e init (daemon en modo setup).
func launchDetached(port int, args []string) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ no sé quién soy: %v\n", err)
		return 1
	}
	logPath := filepath.Join(ident.ConfigDir(), "harnessd.log")
	_ = os.MkdirAll(ident.ConfigDir(), 0o700)
	logf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ log: %v\n", err)
		return 1
	}
	defer logf.Close()
	cmd := exec.Command(exe, args...)
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

// openStore decide el almacén y lo abre (corriendo sus migraciones). Si el
// usuario conectó Postgres desde el panel (hay un DSN en ConfigDir/postgres-token),
// usa Postgres; si no, SQLite en ConfigDir/harness.db — el comportamiento de
// siempre. Devuelve también una etiqueta legible para /health y la tarjeta de
// Conexiones (la ruta del archivo en SQLite, "postgres" en PG). El DSN JAMÁS se
// devuelve ni se loguea: es un secreto write-only como los demás tokens.
//
// El almacén se elige AL ARRANCAR: conectar Postgres desde el panel guarda el
// DSN pero no migra al vuelo el daemon vivo (cambiar de motor con el colector
// escribiendo es justo lo peligroso que no hacemos). El panel pide reiniciar.
func openStore() (*store.Store, string, error) {
	sqlitePath := filepath.Join(ident.ConfigDir(), "harness.db")
	if b, err := os.ReadFile(filepath.Join(ident.ConfigDir(), "postgres-token")); err == nil {
		if dsn := strings.TrimSpace(string(b)); dsn != "" {
			st, err := store.Open(dsn)
			if err != nil {
				return nil, "", fmt.Errorf("postgres configurado pero no abre: %w", err)
			}
			return st, "postgres", nil
		}
	}
	st, err := store.Open(sqlitePath)
	return st, sqlitePath, err
}

// buildSnapshot arma el snapshot que sirve el panel según el target elegido:
//   - LOCAL: lee la DB de esta máquina (api.Build) + herdr local + liveness.
//   - REMOTO (un VPS): trae el snapshot COMPLETO del VPS por SSH
//     (`harnessd snapshot`) + su herdr + liveness contra su herdr. Así el
//     selector de máquina muta TODA la página (tareas, sesiones, costo), no sólo
//     las terminales. Si el VPS no responde harnessd, muestra vacío + warning
//     (las terminales sí siguen, van por otro canal).
// remoteSnapshot arma el snapshot COMPLETO de un target por SSH. También lo
// usa el modo setup post-instalación remota: el harness vive en el VPS y el
// panel local lo observa proxeado, sin workspace local (ADR-0011).
func remoteSnapshot(tgt api.Target) *api.Snapshot {
	hs := herdr.Remote(tgt.SSH).Snapshot() // las terminales del VPS
	var snap *api.Snapshot
	if raw, err := herdr.RemoteHarnessdSnapshot(tgt.SSH, tgt.Path); err == nil {
		var s api.Snapshot
		if json.Unmarshal(raw, &s) == nil {
			snap = &s
		}
	}
	if snap == nil {
		snap = api.EmptySnapshot()
		snap.Warning = "no pude traer los datos de «" + tgt.Name + "» — ¿harnessd instalado y corriendo en el VPS? (sus terminales sí funcionan)"
	}
	api.EnrichLiveness(snap, hs)
	snap.Herdr = hs
	snap.Targets = api.LoadTargets()
	snap.TS = time.Now().Unix()
	return snap
}

func buildSnapshot(r *http.Request, db *store.Store, w ident.Workspace) *api.Snapshot {
	tgt, ok := api.ResolveTargetFull(r.URL.Query().Get("target"))
	if !ok {
		tgt = api.Target{} // destino desconocido → local
	}
	if tgt.SSH != "" {
		return remoteSnapshot(tgt)
	}
	// LOCAL
	snap, err := api.Build(db, w.ID, w.Path, time.Now().Unix())
	if err != nil {
		snap = api.EmptySnapshot()
		snap.Warning = "error leyendo el almacén local: " + err.Error()
	}
	localH := herdr.Local().Snapshot()
	api.EnrichLiveness(snap, localH)
	snap.Herdr = localH
	snap.Targets = api.LoadTargets()
	snap.Connections = api.Connections()
	snap.Workspace.Name = w.Name
	return snap
}

// snapshotCmd imprime el snapshot del workspace como JSON y termina. Es lo que
// el daemon LOCAL corre por SSH en un VPS (`ssh vps harnessd snapshot --json`)
// para que el selector de máquina mute TODA la página con datos del remoto.
// Sólo lectura: abre la DB (que el daemon del VPS mantiene), Build, imprime.
func snapshotCmd(wsPath string) int {
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
	st, _, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ almacén: %v\n", err)
		return 1
	}
	defer st.Close()
	snap, err := api.Build(st, w.ID, w.Path, time.Now().Unix())
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	snap.Workspace.Name = w.Name
	if err := json.NewEncoder(os.Stdout).Encode(snap); err != nil {
		return 1
	}
	return 0
}

func run(port int, wsPath string, setup bool) int {
	started := time.Now()
	m, err := ident.ThisMachine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	// Late binding del workspace (ADR-0011): en modo setup el daemon arranca
	// SIN workspace y el paso 1 del wizard lo fija UNA vez. Un proceso, UN
	// workspace (ADR-0005) — solo que se asigna tarde, sin relanzar (relanzar
	// rotaría el op-token del HTML y mataría la página del wizard).
	var wsVal atomic.Pointer[ident.Workspace]
	if !setup {
		w, err := ident.ResolveWorkspace(wsPath, m.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			return 1
		}
		wsVal.Store(&w)
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

	// Fase 3: el colector. El almacén abre (y migra) ANTES de anunciar salud:
	// un daemon que dice "ok" sin poder escribir es un daemon que miente.
	// openStore elige el motor: Postgres si está conectado, SQLite si no.
	st, dbPath, err := openStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ almacén: %v\n", err)
		return 1
	}
	defer st.Close()
	mode := "all-in-one"
	if setup {
		mode = "setup"
	}
	health := lock.Health{
		Name: "harnessd", Version: Version, Protocol: lock.Protocol,
		PID: os.Getpid(), Mode: mode, Started: started.Unix(),
		DB: dbPath,
	}
	if st.Dialect == "postgres" {
		fmt.Println("   almacén: Postgres (conectado desde el panel)")
	}
	_ = st.SeedBuiltinPrices() // no pisa ediciones del usuario; sin esto todo costo es NULL

	// authOp existe desde el primer byte servido: su token viaja en el HTML.
	// Con WS vacío solo sirve para Guard; el Op real nace al adoptar workspace.
	authOp := api.NewOp("", "", st, port)
	var opPtr atomic.Pointer[api.Op]
	collectQuit := make(chan struct{})
	var collectOnce sync.Once
	startCollect := func(w ident.Workspace) {
		collectOnce.Do(func() {
			col := collect.New(st, m, w)
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
		})
	}
	adopt := func(path string) error {
		w, err := ident.ResolveWorkspace(path, m.ID)
		if err != nil {
			return err
		}
		wsVal.Store(&w)
		opPtr.Store(api.NewOpWithToken(authOp.Token, w.Path, w.ID, st, port))
		startCollect(w)
		return nil
	}
	var mgr *initflow.Manager
	if setup {
		mgr = initflow.New(Version, adopt)
	} else {
		w := *wsVal.Load()
		opPtr.Store(api.NewOpWithToken(authOp.Token, w.Path, w.ID, st, port))
		startCollect(w)
		mgr = initflow.Attach(Version, w.Path, adopt) // nil si no hay init a medias
	}
	if mgr != nil {
		mgr.SetTargetResolver(api.ResolveTarget)
	}
	// precios de modelos observados sin precio: sync automático al arranque
	// (fail-open, en background — sin red simplemente no pasa nada)
	go func() {
		time.Sleep(8 * time.Second) // deja que el colector observe modelos primero
		if added, _, err := api.SyncPrices(st); err == nil && len(added) > 0 {
			fmt.Printf("   precios sincronizados desde OpenRouter: %v\n", added)
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
		wsName := ""
		if w := wsVal.Load(); w != nil {
			wsName = w.Name
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"rows": counts, "costs": costs, "workspace": wsName, "db": dbPath,
		})
	})
	// makeSnap: el snapshot según haya o no workspace adoptado. En modo setup
	// (aún sin workspace) el panel vive solo del wizard: snapshot vacío + init.
	makeSnap := func(r *http.Request) *api.Snapshot {
		var snap *api.Snapshot
		if w := wsVal.Load(); w != nil {
			snap = buildSnapshot(r, st, *w)
		} else if tgt, ok := api.ResolveTargetFull(r.URL.Query().Get("target")); ok && tgt.SSH != "" {
			// sin workspace local pero mirando un VPS (instalación remota
			// terminada): el panel proxea el snapshot del target
			snap = remoteSnapshot(tgt)
		} else {
			snap = api.EmptySnapshot()
			snap.TS = time.Now().Unix()
			snap.Mode = "setup"
			snap.Targets = api.LoadTargets()
			snap.Connections = api.Connections()
		}
		if mgr != nil {
			snap.Init = mgr.Public()
		}
		return snap
	}
	// Fase 1: el daemon ES el backend del panel. Sirve el snapshot (leído del
	// SQLite) y el build de React embebido. Un proceso, todos los workspaces.
	mux.HandleFunc("/api/state", func(rw http.ResponseWriter, r *http.Request) {
		if !api.GuardRead(rw, r) {
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(rw).Encode(makeSnap(r))
	})
	// SSE: el frontend espera un evento "snapshot" con el estado entero. El
	// colector ya escribe cada 2 s; aquí re-emitimos el snapshot al mismo ritmo.
	mux.HandleFunc("/api/stream", func(rw http.ResponseWriter, r *http.Request) {
		if !api.GuardRead(rw, r) {
			return
		}
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
			b, _ := json.Marshal(makeSnap(r))
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
		w := wsVal.Load()
		if w == nil {
			_ = json.NewEncoder(rw).Encode([]any{})
			return
		}
		_ = json.NewEncoder(rw).Encode(api.TaskEvents(st, w.ID, r.URL.Query().Get("task")))
	})
	mux.HandleFunc("/api/task-git", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		w := wsVal.Load()
		if w == nil {
			_ = json.NewEncoder(rw).Encode(map[string]any{})
			return
		}
		_ = json.NewEncoder(rw).Encode(api.BuildTaskGit(w.Path, r.URL.Query().Get("task")))
	})
	mux.HandleFunc("/api/session", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		d, err := api.BuildSession(st, r.URL.Query().Get("id"), time.Now().Unix())
		if err != nil {
			rw.WriteHeader(500)
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(rw).Encode(d)
	})
	// herdr (OPCIONAL): el estado vivo de las terminales de agentes. Si herdr no
	// está o su server no corre, devuelve available:false — la vista se degrada.
	// Cross-workspace a propósito: ves todo lo que corre en la máquina.
	mux.HandleFunc("/api/herdr", func(rw http.ResponseWriter, r *http.Request) {
		if !api.GuardRead(rw, r) {
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		ssh, _ := api.ResolveTarget(r.URL.Query().Get("target"))
		_ = json.NewEncoder(rw).Encode(herdr.Remote(ssh).Snapshot())
	})
	// El plano de OPERAR (ADR-0010) — crear trabajo, jamás merges. Con late
	// binding: hasta que el paso 1 del init fija workspace, estas rutas dan 409.
	opH := func(f func(*api.Op, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(rw http.ResponseWriter, r *http.Request) {
			o := opPtr.Load()
			if o == nil {
				rw.Header().Set("Content-Type", "application/json")
				rw.WriteHeader(http.StatusConflict)
				_, _ = rw.Write([]byte(`{"ok":false,"error":"workspace no fijado — completa el paso 1 del init"}`))
				return
			}
			f(o, rw, r)
		}
	}
	mux.HandleFunc("/api/op/task", opH((*api.Op).OpTask))
	mux.HandleFunc("/api/op/respond", opH((*api.Op).OpRespond))
	mux.HandleFunc("/api/op/pane-send", opH((*api.Op).OpPaneSend))
	mux.HandleFunc("/api/op/herdr", opH((*api.Op).OpHerdr))
	mux.HandleFunc("/api/op/herdr-key", opH((*api.Op).OpHerdrKey))
	mux.HandleFunc("/api/op/herdr-open", opH((*api.Op).OpHerdrOpen))
	// targets = config GLOBAL (~/.config/harness/targets.json), sin workspace:
	// el wizard debe poder dar de alta un VPS ANTES del paso 1 (authOp trae
	// el mismo Guard de token+Host; emit es no-op sin workspace).
	mux.HandleFunc("/api/op/targets", authOp.OpTargets)
	mux.HandleFunc("/api/op/archive", opH((*api.Op).OpArchive))
	mux.HandleFunc("/api/op/probe-mcp", opH((*api.Op).OpProbeMcp))
	mux.HandleFunc("/api/op/connect", opH((*api.Op).OpConnect))
	mux.HandleFunc("/api/op/sync-prices", opH((*api.Op).OpSyncPrices))
	// ratify funciona SIN workspace local cuando apunta a un target (la firma
	// corre en el VPS por ssh): cae a authOp en modo setup
	mux.HandleFunc("/api/op/ratify", func(rw http.ResponseWriter, r *http.Request) {
		o := opPtr.Load()
		if o == nil {
			o = authOp
		}
		o.OpRatify(rw, r)
	})

	// El plano de INIT (ADR-0011): el wizard de onboarding. Mismo Guard que
	// operar (Host + token del HTML); la lógica vive en initflow.Manager.
	if mgr != nil {
		mux.HandleFunc("/api/init/state", func(rw http.ResponseWriter, r *http.Request) {
			if !api.GuardRead(rw, r) {
				return
			}
			rw.Header().Set("Content-Type", "application/json")
			rw.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(rw).Encode(map[string]any{"active": true, "state": mgr.Public()})
		})
		mux.HandleFunc("/api/init/logs", func(rw http.ResponseWriter, r *http.Request) {
			if !api.GuardRead(rw, r) {
				return
			}
			fl, ok := rw.(http.Flusher)
			if !ok {
				http.Error(rw, "sin streaming", http.StatusInternalServerError)
				return
			}
			var after int64
			fmt.Sscanf(r.Header.Get("Last-Event-ID"), "%d", &after)
			replay, ch, cancel := mgr.Logs().Subscribe(after)
			defer cancel()
			rw.Header().Set("Content-Type", "text/event-stream")
			rw.Header().Set("Cache-Control", "no-store")
			rw.Header().Set("Connection", "keep-alive")
			emit := func(ll initflow.LogLine) bool {
				b, _ := json.Marshal(ll)
				if _, err := fmt.Fprintf(rw, "id: %d\nevent: log\ndata: %s\n\n", ll.Seq, b); err != nil {
					return false
				}
				fl.Flush()
				return true
			}
			for _, ll := range replay {
				if !emit(ll) {
					return
				}
			}
			beat := time.NewTicker(15 * time.Second)
			defer beat.Stop()
			for {
				select {
				case <-r.Context().Done():
					return
				case ll := <-ch:
					if !emit(ll) {
						return
					}
				case <-beat.C:
					if _, err := fmt.Fprint(rw, ": beat\n\n"); err != nil {
						return
					}
					fl.Flush()
				}
			}
		})
		mux.HandleFunc("/api/op/init/", func(rw http.ResponseWriter, r *http.Request) {
			body, ok := authOp.Guard(rw, r)
			if !ok {
				return
			}
			action := strings.TrimPrefix(r.URL.Path, "/api/op/init/")
			res, code := mgr.Handle(action, body)
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(code)
			_ = json.NewEncoder(rw).Encode(res)
		})
	}
	mux.HandleFunc("/api/db", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(api.DBInfo(st, dbPath))
	})
	mux.HandleFunc("/api/herdr/pane", func(rw http.ResponseWriter, r *http.Request) {
		if !api.GuardRead(rw, r) {
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		ssh, _ := api.ResolveTarget(r.URL.Query().Get("target"))
		id := r.URL.Query().Get("id")
		txt, err := herdr.Remote(ssh).PaneRead(id, 200, r.URL.Query().Get("fmt"))
		if err != nil {
			_ = json.NewEncoder(rw).Encode(map[string]string{"error": "no pude leer el pane"})
			return
		}
		herdr.AccumulateVisible(ssh, id, txt) // backlog para shells (ruta B)
		_ = json.NewEncoder(rw).Encode(map[string]string{"text": txt})
	})
	// Historial COMPLETO de una terminal: si el pane corre un agente (Claude
	// Code…), su transcripción JSONL real por session id (ruta A); si es un shell,
	// el backlog de pantalla acumulado (ruta B). "Siempre tener todo".
	mux.HandleFunc("/api/herdr/history", func(rw http.ResponseWriter, r *http.Request) {
		if !api.GuardRead(rw, r) {
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		ssh, _ := api.ResolveTarget(r.URL.Query().Get("target"))
		pane := r.URL.Query().Get("pane")
		session := r.URL.Query().Get("session")
		kind := "backlog"
		var txt string
		if session != "" {
			if t, _ := herdr.Remote(ssh).Transcript(session); t != "" {
				txt, kind = t, "transcript"
			}
		}
		if txt == "" {
			txt = herdr.Backlog(ssh, pane)
		}
		_ = json.NewEncoder(rw).Encode(map[string]string{"text": txt, "kind": kind})
	})

	// El build de React (embebido). Va AL FINAL: solo atrapa lo que no matchea
	// /api ni /health ni /admin.
	web := webui.Handler(authOp.Token)
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
	if w := wsVal.Load(); w == nil {
		fmt.Printf("   modo setup — sin workspace aún: el wizard lo fija → http://127.0.0.1:%d/#/init\n", port)
	} else if w.Local {
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
