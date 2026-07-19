package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andresgarcia29/harness-daemon/internal/api"
	"github.com/andresgarcia29/harness-daemon/internal/ident"
)

// taskGitCmd / taskEventsCmd — el drill-down de una tarea como CLI: lo que el
// panel local proxea por ssh cuando la tarea vive en un VPS (ADR-0011 §4).
// Stdout = JSON con el MISMO shape que los endpoints HTTP.
func taskGitCmd(args []string) int {
	fs := flag.NewFlagSet("task-git", flag.ExitOnError)
	ws := fs.String("workspace", ".", "workspace")
	taskID := fs.String("task", "", "id de la tarea")
	_ = fs.Parse(args)
	abs, _ := filepath.Abs(*ws)
	if err := json.NewEncoder(os.Stdout).Encode(api.BuildTaskGit(abs, *taskID)); err != nil {
		return 1
	}
	return 0
}

func taskEventsCmd(args []string) int {
	fs := flag.NewFlagSet("task-events", flag.ExitOnError)
	ws := fs.String("workspace", ".", "workspace")
	taskID := fs.String("task", "", "id de la tarea")
	_ = fs.Parse(args)
	m, err := ident.ThisMachine()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}
	w, err := ident.ResolveWorkspace(*ws, m.ID)
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
	if err := json.NewEncoder(os.Stdout).Encode(api.TaskEvents(st, w.ID, *taskID)); err != nil {
		return 1
	}
	return 0
}
