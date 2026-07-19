package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andresgarcia29/harness-daemon/internal/api"
)

// ratifyCmd — `harness ratify`: firma la ley DRAFT de una instancia (el flip
// DRAFT→RATIFIED, nada más). Es el brazo CLI del panel: en remoto, el panel
// local lo invoca por ssh (mismo código, ADR-0011 §4).
func ratifyCmd(args []string) int {
	fs := flag.NewFlagSet("ratify", flag.ExitOnError)
	ws := fs.String("workspace", ".", "workspace de la instancia")
	path := fs.String("path", "", "documento a ratificar (relativo al workspace)")
	all := fs.Bool("all", false, "ratificar TODOS los DRAFT")
	list := fs.Bool("list", false, "solo listar los DRAFT")
	asJSON := fs.Bool("json", false, "salida JSON")
	_ = fs.Parse(args)
	abs, err := filepath.Abs(*ws)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	emit := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }

	if *list || (!*all && *path == "") {
		drafts := api.ListDrafts(abs)
		if *asJSON {
			emit(map[string]any{"ok": true, "drafts": drafts})
		} else {
			for _, d := range drafts {
				fmt.Printf("  DRAFT  %-12s %s\n", d.Kind, d.Path)
			}
			fmt.Printf("── %d documento(s) en DRAFT — ratifica con --path <p> o --all\n", len(drafts))
		}
		return 0
	}
	var done, failed []string
	if *all {
		for _, d := range api.ListDrafts(abs) {
			if err := api.RatifyDoc(abs, d.Path); err != nil {
				failed = append(failed, d.Path+": "+err.Error())
			} else {
				done = append(done, d.Path)
			}
		}
	} else {
		if err := api.RatifyDoc(abs, *path); err != nil {
			if *asJSON {
				emit(map[string]any{"ok": false, "error": err.Error()})
			} else {
				fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			}
			return 1
		}
		done = append(done, *path)
	}
	if *asJSON {
		emit(map[string]any{"ok": len(failed) == 0, "ratified": done, "failed": failed,
			"drafts": api.ListDrafts(abs)})
	} else {
		for _, p := range done {
			fmt.Printf("  ✓ RATIFIED %s\n", p)
		}
		for _, f := range failed {
			fmt.Printf("  ❌ %s\n", f)
		}
	}
	if len(failed) > 0 {
		return 1
	}
	return 0
}
