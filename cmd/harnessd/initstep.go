package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
	"github.com/andresgarcia29/harness-daemon/internal/initflow"
)

// initStepCmd — `harness init-step <paso>`: los pasos del wizard como
// subcomandos headless. Es lo que el wizard LOCAL corre en un VPS vía
// `ssh <target> harness init-step …` (ADR-0011 §4): el mismo código, allá.
//
// Contrato de streams: stderr = progreso humano (el orquestador lo streamea
// a su LogBuffer), stdout = resultado JSON. Los secretos llegan por archivos
// del ConfigDir remoto o stdin — jamás por argv.
func initStepCmd(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "uso: harness init-step <workspace|requirements|clone|archaeology|first-task|finish> [flags]")
		return 2
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("init-step "+sub, flag.ExitOnError)
	ws := fs.String("workspace", ".", "workspace")
	path := fs.String("path", "", "ruta del workspace (sub workspace)")
	create := fs.Bool("create", false, "crear la carpeta si no existe")
	dry := fs.Bool("dry-run", false, "solo validar")
	outside := fs.Bool("confirm-outside-home", false, "permitir ruta fuera del home")
	source := fs.String("source", "pat", "fuente del token de GitHub: gh | pat")
	repos := fs.String("repos", "", "selección: owner/repo[@ref],owner/repo2…")
	all := fs.Bool("all", false, "probe-mcp: sondar todo el catálogo")
	name := fs.String("name", "", "probe-mcp: sondar UN MCP por nombre")
	key := fs.String("key", "", "probe-mcp: clave del secreto (el VALOR llega por stdin)")
	store := fs.Bool("store", false, "probe-mcp: persistir el secreto en <ws>/.secrets si la sonda contesta")
	_ = fs.Bool("json", true, "resultado JSON en stdout (siempre)")
	_ = fs.Parse(rest)
	log := func(s string) { fmt.Fprintln(os.Stderr, s) }
	emit := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }

	switch sub {
	case "workspace":
		res, code := initflow.WorkspaceStep(*path, *create, *dry, *outside)
		emit(res)
		if code >= 400 {
			return 1
		}
		return 0

	case "requirements":
		reqs := initflow.CheckBaseline()
		emit(map[string]any{"ok": true, "requirements": reqs})
		return 0

	case "clone":
		abs, _ := filepath.Abs(*ws)
		var sel []initflow.RepoSel
		for _, p := range strings.Split(*repos, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			full, ref, _ := strings.Cut(p, "@")
			sel = append(sel, initflow.RepoSel{FullName: full, Ref: ref, Status: initflow.Pending})
		}
		fails, err := initflow.CloneRepos(abs, *source, sel, log,
			func(i int, s initflow.Status, e string) {
				sel[i].Status, sel[i].Error = s, e
				// progreso estructurado por stderr: el orquestador LOCAL lo
				// parsea y actualiza los checks por repo EN VIVO (no al final)
				fmt.Fprintf(os.Stderr, "@@repo|%s|%s|%s\n", sel[i].FullName, s, strings.ReplaceAll(e, "\n", " "))
			})
		emit(map[string]any{"ok": err == nil, "repos": sel, "fails": fails})
		if err != nil {
			log("❌ " + err.Error())
			return 1
		}
		return 0

	case "archaeology":
		abs, _ := filepath.Abs(*ws)
		raw, err := os.ReadFile(filepath.Join(abs, "harness-answers.yaml"))
		if err != nil {
			log("❌ no hay harness-answers.yaml — genera primero")
			return 1
		}
		a, err := gen.ParseAnswersYAML(raw)
		if err != nil {
			log("❌ answers: " + err.Error())
			return 1
		}
		results, fails := initflow.RunArchaeology(abs, a, Version, log, func(int, initflow.ArchState) {})
		emit(map[string]any{"ok": fails == 0, "results": results, "fails": fails})
		if fails > 0 {
			return 1
		}
		return 0

	case "probe-mcp":
		abs, _ := filepath.Abs(*ws)
		slug := ""
		if raw, err := os.ReadFile(filepath.Join(abs, "harness-answers.yaml")); err == nil {
			if a, err := gen.ParseAnswersYAML(raw); err == nil {
				slug = a.Project.Name
			}
		}
		if *all {
			probes, waiting, used := initflow.ProbeAllIn(abs, slug, log)
			emit(map[string]any{"ok": true, "probes": probes, "waiting_secret": waiting, "used_keys": used})
			return 0
		}
		if *name == "" {
			log("❌ falta --name o --all")
			return 2
		}
		value := ""
		if *key != "" { // el VALOR por stdin: jamás en argv (ps lo vería)
			b, err := io.ReadAll(os.Stdin)
			if err != nil || len(strings.TrimSpace(string(b))) == 0 {
				log("❌ --key requiere el valor por stdin")
				return 2
			}
			value = strings.TrimSpace(string(b))
		}
		probe, stored, err := initflow.ProbeOneIn(abs, slug, *name, *key, value, *store)
		if err != nil {
			emit(map[string]any{"ok": false, "error": err.Error(), "probe": probe})
			return 1
		}
		emit(map[string]any{"ok": probe.OK, "probe": probe, "stored": stored})
		if !probe.OK {
			return 1
		}
		return 0

	case "first-task":
		abs, _ := filepath.Abs(*ws)
		n := initflow.CountTasks(abs)
		emit(map[string]any{"ok": n > 0, "count": n})
		return 0

	case "finish":
		abs, _ := filepath.Abs(*ws)
		if err := initflow.RunDoctor(abs, log); err != nil {
			log("❌ " + err.Error())
			emit(map[string]any{"ok": false, "error": err.Error()})
			return 1
		}
		emit(map[string]any{"ok": true})
		return 0

	default:
		fmt.Fprintf(os.Stderr, "init-step desconocido: %s\n", sub)
		return 2
	}
}
