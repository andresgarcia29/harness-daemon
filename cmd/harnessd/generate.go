package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
)

// generateCmd — `harness generate`: el generador determinista, 100%
// scripteable (el wizard es una piel sobre esto). Lee answers de --answers
// (YAML del esquema fijo, o JSON) o del harness-answers.yaml de la instancia
// (modo update), exige inventory.json (harness discover), y aplica la
// política de idempotencia: lo nuestro se actualiza, lo personalizado → .new.
func generateCmd(args []string) int {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	ws := fs.String("workspace", ".", "workspace destino")
	answersPath := fs.String("answers", "", "answers a usar (yaml del esquema fijo o json); default: harness-answers.yaml de la instancia")
	force := fs.Bool("force", false, "pisar archivos personalizados (sin .new)")
	dry := fs.Bool("dry-run", false, "no escribir: solo reportar qué haría")
	asJSON := fs.Bool("json", false, "reporte como JSON")
	_ = fs.Parse(args)

	abs, err := filepath.Abs(*ws)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	ap := *answersPath
	if ap == "" {
		ap = filepath.Join(abs, "harness-answers.yaml")
	}
	var raw []byte
	if ap == "-" { // answers por stdin (el camino remoto: jamás en argv ni en disco intermedio)
		b, err := io.ReadAll(os.Stdin)
		if err != nil || len(b) == 0 {
			fmt.Fprintln(os.Stderr, "❌ --answers -: no llegó nada por stdin")
			return 1
		}
		raw = b
	} else {
		b, err := os.ReadFile(ap)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ no pude leer answers en %s — pasa --answers o corre el wizard (harness init)\n", ap)
			return 1
		}
		raw = b
	}
	var a *gen.Answers
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		a = &gen.Answers{}
		if err := json.Unmarshal(raw, a); err != nil {
			fmt.Fprintf(os.Stderr, "❌ answers JSON: %v\n", err)
			return 1
		}
		if err := a.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			return 1
		}
	} else {
		if a, err = gen.ParseAnswersYAML(raw); err != nil {
			fmt.Fprintf(os.Stderr, "❌ answers YAML: %v\n", err)
			return 1
		}
	}
	inv, err := gen.LoadInventory(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ falta %s/inventory.json — corre `harness discover --workspace %s` primero\n", abs, abs)
		return 1
	}
	// auto-cura de instancias viejas: sin NINGUNA capacidad CLI en answers
	// (el bug del bootstrap vacío), siembra las detectadas por señal
	hasCLI := false
	for _, c := range a.Capabilities {
		if c.Bin != "" {
			hasCLI = true
		}
	}
	if !hasCLI {
		seeded := gen.SeedCapabilities(inv)
		if len(seeded) > 0 {
			a.Capabilities = append(a.Capabilities, seeded...)
			fmt.Fprintf(os.Stderr, "ℹ️  %d capacidades CLI sembradas del catálogo (toolchains, gitleaks, ccusage…) — el bootstrap las instalará\n", len(seeded))
		}
	}
	rep, err := gen.Generate(a, inv, gen.Opts{WS: abs, Version: Version, Now: time.Now(), Force: *force, DryRun: *dry})
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(rep)
		return 0
	}
	for _, f := range rep.Files {
		mark := map[string]string{"created": "＋", "updated": "↻", "kept": "·", "conflict": "⚠︎"}[f.Action]
		if f.Action != "kept" {
			fmt.Printf("  %s %s\n", mark, f.Path)
		}
	}
	fmt.Printf("✅ generate: %d creados · %d actualizados · %d intactos · %d conflictos (.new)\n",
		rep.Created, rep.Updated, rep.Kept, rep.Conflicts)
	if rep.Conflicts > 0 {
		fmt.Println("   revisa los .new: tu versión local se conservó (usa --force para pisar)")
	}
	return 0
}

// discoverCmd — `harness discover`: el script determinista embebido, como
// subcomando (por SSH en un VPS es el MISMO comando — ADR-0011 §4).
func discoverCmd(args []string) int {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	ws := fs.String("workspace", ".", "workspace a escanear")
	asJSON := fs.Bool("json", false, "imprimir inventory.json a stdout")
	_ = fs.Parse(args)
	abs, _ := filepath.Abs(*ws)
	script, err := gen.Asset("scripts/discover.sh")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	tmp := filepath.Join(abs, ".harness", "init")
	_ = os.MkdirAll(tmp, 0o755)
	path := filepath.Join(tmp, "discover.sh")
	if err := os.WriteFile(path, script, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	cmd := exec.Command("bash", path, abs)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr // el resumen humano a stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	if *asJSON {
		b, err := os.ReadFile(filepath.Join(abs, "inventory.json"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			return 1
		}
		os.Stdout.Write(b)
	}
	return 0
}
