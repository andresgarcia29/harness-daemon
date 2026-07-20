// inventory.go — lo que el daemon lee de los ARCHIVOS del workspace para las
// vistas Docs y Skills & MCP, y la metadata de git por tarea. Es el port fiel
// del panel de Python (scan_toolbox / mcp_servers / task_git / task_events):
// nada de prosa inventada; si un archivo no está, no aparece.
package api

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/store"
)

type CmdDoc struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
	Args string `json:"args"`
}
type AgentDoc struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}
type MakeDoc struct {
	Target string `json:"target"`
	Desc   string `json:"desc"`
}
type SkillDoc struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
	OK   bool   `json:"ok"`
}
type Toolbox struct {
	Version  string     `json:"version"`
	Commands []CmdDoc   `json:"commands"`
	Agents   []AgentDoc `json:"agents"`
	Make     []MakeDoc  `json:"make"`
	Gates    []string   `json:"gates"`
	Hooks    []string   `json:"hooks"`
	Skills   []SkillDoc `json:"skills"`
}
type McpServer struct {
	Name      string    `json:"name"`
	Command   string    `json:"command"`
	Args      []string  `json:"args"`
	Wrapped   bool      `json:"wrapped"`
	BinOK     bool      `json:"bin_ok"`
	SecretsOK *bool     `json:"secrets_ok"`
	Env       []string  `json:"env"`
	Probe     *McpProbe `json:"probe"` // la sonda viva (OPERAR): se llena al "Probar"
}

// McpProbe: resultado del handshake JSON-RPC real contra un servidor MCP.
// "funciona" = arrancó y contestó initialize; tools = lo que expone de verdad.
type McpProbe struct {
	OK       bool     `json:"ok"`
	Ms       int64    `json:"ms"`
	Server   string   `json:"server,omitempty"`
	Version  string   `json:"version,omitempty"`
	Error    string   `json:"error,omitempty"`
	AuthHint bool     `json:"auth_hint,omitempty"`
	At       string   `json:"at,omitempty"`
	Tools    []string `json:"tools,omitempty"`
}

// frontmatter YAML simple: description / argument-hint / name.
func frontmatter(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return out
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if i := strings.Index(line, ":"); i > 0 && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			k := strings.TrimSpace(line[:i])
			v := strings.Trim(strings.TrimSpace(line[i+1:]), `"'`)
			out[k] = v
		}
	}
	return out
}

var makeRe = regexp.MustCompile(`^([a-z][a-z0-9-]*):.*?## (.+)$`)
var gateRe = regexp.MustCompile(`\bgate_[a-z_]+`)

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// BuildToolbox arma el inventario del workspace. nil si el workspace no tiene
// harness (sin commands, sin Makefile) — el frontend enseña su vacío.
func BuildToolbox(ws string) *Toolbox {
	tb := &Toolbox{Commands: []CmdDoc{}, Agents: []AgentDoc{}, Make: []MakeDoc{},
		Gates: []string{}, Hooks: []string{}, Skills: []SkillDoc{}}
	if b, err := os.ReadFile(filepath.Join(ws, ".harness-version")); err == nil {
		tb.Version = strings.TrimSpace(string(b))
	}
	cmds, _ := filepath.Glob(filepath.Join(ws, ".claude", "commands", "*.md"))
	sort.Strings(cmds)
	for _, f := range cmds {
		fm := frontmatter(f)
		tb.Commands = append(tb.Commands, CmdDoc{
			Name: "/" + strings.TrimSuffix(filepath.Base(f), ".md"),
			Desc: fm["description"], Args: fm["argument-hint"]})
	}
	ags, _ := filepath.Glob(filepath.Join(ws, ".claude", "agents", "*.md"))
	sort.Strings(ags)
	for _, f := range ags {
		fm := frontmatter(f)
		tb.Agents = append(tb.Agents, AgentDoc{
			Name: strings.TrimSuffix(filepath.Base(f), ".md"), Desc: clip(fm["description"], 180)})
	}
	sk, _ := filepath.Glob(filepath.Join(ws, ".claude", "skills", "*"))
	sort.Strings(sk)
	for _, d := range sk {
		p := filepath.Join(d, "SKILL.md")
		if _, err := os.Stat(p); err == nil {
			fm := frontmatter(p)
			name := fm["name"]
			if name == "" {
				name = filepath.Base(d)
			}
			tb.Skills = append(tb.Skills, SkillDoc{Name: name, Desc: clip(fm["description"], 180), OK: len(fm) > 0})
		}
	}
	if f, err := os.Open(filepath.Join(ws, "Makefile")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if m := makeRe.FindStringSubmatch(sc.Text()); m != nil {
				tb.Make = append(tb.Make, MakeDoc{Target: m[1], Desc: strings.TrimSpace(m[2])})
			}
		}
		f.Close()
	}
	if b, err := os.ReadFile(filepath.Join(ws, "scripts", "ship.sh")); err == nil {
		seen := map[string]bool{}
		for _, g := range gateRe.FindAllString(string(b), -1) {
			if !seen[g] {
				seen[g] = true
				tb.Gates = append(tb.Gates, g)
			}
		}
		sort.Strings(tb.Gates)
	}
	tb.Hooks = hooksFrom(filepath.Join(ws, ".claude", "settings.json"))
	if len(tb.Commands) == 0 && len(tb.Make) == 0 && len(tb.Agents) == 0 {
		return nil
	}
	return tb
}

func hooksFrom(path string) []string {
	out := []string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return out
	}
	seen := map[string]bool{}
	for _, entries := range cfg.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				if h.Command == "" {
					continue
				}
				name := filepath.Base(strings.Fields(h.Command)[0])
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// BuildMcp lee .mcp.json y hace los checks ESTÁTICOS (binario en PATH, .secrets
// para los envueltos en with-secrets). La sonda viva es OPERAR → probe:nil.
func BuildMcp(ws string) []McpServer {
	out := []McpServer{}
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
	names := make([]string, 0, len(cfg.Servers))
	for n := range cfg.Servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sv := cfg.Servers[n]
		wrapped := strings.Contains(sv.Command, "with-secrets")
		realbin := sv.Command
		if wrapped && len(sv.Args) > 0 {
			realbin = sv.Args[0]
		}
		_, lookErr := exec.LookPath(realbin)
		binOK := lookErr == nil
		if wrapped {
			wp := sv.Command
			if !filepath.IsAbs(wp) {
				wp = filepath.Join(ws, sv.Command)
			}
			if _, e := os.Stat(wp); e != nil {
				binOK = false
			}
		}
		var secretsOK *bool
		if wrapped {
			_, e := os.Stat(filepath.Join(ws, ".secrets"))
			v := e == nil
			secretsOK = &v
		}
		env := make([]string, 0, len(sv.Env))
		for k := range sv.Env {
			env = append(env, k)
		}
		sort.Strings(env)
		args := sv.Args
		if len(args) > 6 {
			args = args[:6]
		}
		out = append(out, McpServer{Name: n, Command: sv.Command, Args: args,
			Wrapped: wrapped, BinOK: binOK, SecretsOK: secretsOK, Env: env, Probe: mcpProbeGet(n)})
	}
	return out
}

// ── git por tarea (port de task_git) ────────────────────────────────────
type RepoGit struct {
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	Ahead       int    `json:"ahead"`
	Dirty       bool   `json:"dirty"`
	LastSubject string `json:"last_subject"`
	LastTS      int64  `json:"last_ts"`
	PR          *struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		URL    string `json:"url"`
	} `json:"pr"`
	PushedDirect bool `json:"pushed_direct"`
}
type TaskGit struct {
	Repos []RepoGit `json:"repos"`
	Read  []string  `json:"read"`
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func git(dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func BuildTaskGit(ws, taskID string) TaskGit {
	tg := TaskGit{Repos: []RepoGit{}, Read: []string{}}
	if strings.ContainsAny(taskID, `/\`) {
		return tg
	}
	touched := map[string]bool{}
	dirs, _ := filepath.Glob(filepath.Join(ws, "worktrees", taskID, "*"))
	sort.Strings(dirs)
	_, ghErr := exec.LookPath("gh")
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, ".git")); err != nil {
			continue
		}
		name := filepath.Base(d)
		touched[name] = true
		branch := git(d, "rev-parse", "--abbrev-ref", "HEAD")
		if branch == "" {
			branch = "?"
		}
		ahead := 0
		if v := git(d, "rev-list", "--count", "origin/main..HEAD"); v != "" {
			for _, c := range v {
				if c >= '0' && c <= '9' {
					ahead = ahead*10 + int(c-'0')
				}
			}
		}
		dirty := git(d, "status", "--porcelain") != ""
		subj, cts := "", int64(0)
		if last := git(d, "log", "-1", "--format=%s%x09%ct"); last != "" {
			parts := strings.SplitN(last, "\t", 2)
			subj = clip(parts[0], 120)
			if len(parts) == 2 {
				for _, c := range parts[1] {
					if c >= '0' && c <= '9' {
						cts = cts*10 + int64(c-'0')
					}
				}
			}
		}
		rg := RepoGit{Repo: name, Branch: branch, Ahead: ahead, Dirty: dirty,
			LastSubject: subj, LastTS: cts, PushedDirect: ahead == 0 && subj != ""}
		if ghErr == nil && branch != "?" && branch != "main" {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			c := exec.CommandContext(ctx, "gh", "pr", "list", "--head", branch,
				"--state", "all", "--json", "number,state,url", "--limit", "1")
			c.Dir = d
			if o, err := c.Output(); err == nil {
				var arr []struct {
					Number int    `json:"number"`
					State  string `json:"state"`
					URL    string `json:"url"`
				}
				if json.Unmarshal(o, &arr) == nil && len(arr) > 0 {
					rg.PR = &struct {
						Number int    `json:"number"`
						State  string `json:"state"`
						URL    string `json:"url"`
					}{arr[0].Number, strings.ToLower(arr[0].State), arr[0].URL}
					rg.PushedDirect = false
				}
			}
			cancel()
		}
		tg.Repos = append(tg.Repos, rg)
	}
	// leídos: del evidence.log, solo filas read/scan/ran-file (rutas, no comandos)
	read := map[string]bool{}
	if f, err := os.Open(filepath.Join(ws, "tasks", taskID, "evidence.log")); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		pref := "worktrees/" + taskID + "/"
		for sc.Scan() {
			parts := strings.Split(sc.Text(), "\t")
			if len(parts) < 4 {
				continue
			}
			if parts[2] != "read" && parts[2] != "scan" && parts[2] != "ran-file" {
				continue
			}
			p := strings.TrimSpace(parts[3])
			var repo string
			if i := strings.Index(p, pref); i >= 0 {
				repo = strings.SplitN(p[i+len(pref):], "/", 2)[0]
			} else if strings.Contains(p, "/") && !strings.HasPrefix(p, "worktrees") &&
				!strings.HasPrefix(p, "/") && !strings.HasPrefix(p, ".") {
				repo = strings.SplitN(p, "/", 2)[0]
			}
			if repo != "" && safeID.MatchString(repo) && !touched[repo] {
				read[repo] = true
			}
		}
		f.Close()
	}
	for r := range read {
		tg.Read = append(tg.Read, r)
	}
	sort.Strings(tg.Read)
	return tg
}

// TaskEvents: TODOS los eventos del bus de una tarea, desde SQLite.
func TaskEvents(db store.Queryer, wsID, taskID string) []Event {
	out := []Event{}
	if strings.ContainsAny(taskID, `/\`) {
		return out
	}
	rows, err := db.Query(`SELECT ts, kind, COALESCE(actor,''), COALESCE(summary,''), ok
		FROM events WHERE workspace_id = ? AND task_id = ? ORDER BY ts ASC LIMIT 500`, wsID, taskID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ts int64
		var kind, actor, summary string
		var ok sql.NullInt64
		if rows.Scan(&ts, &kind, &actor, &summary, &ok) != nil {
			continue
		}
		e := Event{TS: time.Unix(ts, 0).UTC().Format(time.RFC3339), Kind: kind,
			Task: taskID, Actor: actor, Summary: summary}
		if ok.Valid {
			b := ok.Int64 != 0
			e.OK = &b
		}
		out = append(out, e)
	}
	return out
}
