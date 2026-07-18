package gen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/andresgarcia29/harness-daemon/internal/ident"
	"github.com/andresgarcia29/harness-daemon/internal/webui"
)

// Report — qué hizo el generador con cada archivo.
type FileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"` // created | updated | kept | conflict | would-create (dry)
}
type Report struct {
	Files     []FileResult `json:"files"`
	Created   int          `json:"created"`
	Updated   int          `json:"updated"`
	Kept      int          `json:"kept"`
	Conflicts int          `json:"conflicts"`
}

// genManifestPath — el registro sha256 de lo que ESTE generador escribió.
// Es la memoria de la idempotencia: archivo actual == sha registrado →
// nuestro, se puede sobreescribir; distinto → personalización del humano →
// .new + conflict (nada se pisa sin --force).
func genManifestPath(ws string) string { return filepath.Join(ws, ".harness", "gen-manifest.json") }

func loadGenManifest(ws string) map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(genManifestPath(ws)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type rendered struct {
	g       GenFile
	content []byte
}

// Generate — TODO se renderiza a memoria primero (un template roto = no se
// escribe NADA), luego se aplica con la política de idempotencia por archivo.
func Generate(a *Answers, inv *Inventory, o Opts) (*Report, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	base := Vars(a, inv, o)
	var todo []rendered

	for _, g := range Files(a, inv, o) {
		var content []byte
		switch {
		case g.Inline != nil:
			content = g.Inline
		case g.Src != "":
			var raw []byte
			var err error
			if len(g.Src) > 0 && g.Src[0] == '@' { // fuera de templates/ (p.ej. scripts/doctor.sh)
				raw, err = Asset(g.Src[1:])
			} else {
				raw, err = Asset("templates/" + g.Src)
			}
			if err != nil {
				return nil, fmt.Errorf("asset %s: %w", g.Src, err)
			}
			if g.Render {
				vars := base
				if len(g.Extra) > 0 {
					vars = make(map[string]string, len(base)+len(g.Extra))
					for k, v := range base {
						vars[k] = v
					}
					for k, v := range g.Extra {
						vars[k] = v
					}
				}
				content, err = Render(g.Dst, raw, vars)
				if err != nil {
					return nil, err
				}
			} else {
				content = raw
			}
		default:
			return nil, fmt.Errorf("entrada sin fuente: %s", g.Dst)
		}
		todo = append(todo, rendered{g: g, content: content})
	}

	// .mcp.json (derivado del catálogo, no template)
	if mcp, err := McpJSON(a); err != nil {
		return nil, err
	} else if mcp != nil {
		todo = append(todo, rendered{g: GenFile{Dst: ".mcp.json", Mode: 0o644}, content: mcp})
	}

	// el panel compilado (dist/) viene del embed de webui — mismo binario,
	// una sola copia de la verdad
	distFiles, err := webui.DistFiles()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(distFiles))
	for p := range distFiles {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		todo = append(todo, rendered{g: GenFile{Dst: "scripts/ui/dist/" + p, Mode: 0o644}, content: distFiles[p]})
	}

	// ── aplicar ──
	rep := &Report{}
	prev := loadGenManifest(o.WS)
	next := map[string]string{}
	for _, r := range todo {
		abs := filepath.Join(o.WS, r.g.Dst)
		newSha := sha(r.content)
		next[r.g.Dst] = newSha
		cur, err := os.ReadFile(abs)
		exists := err == nil
		action := ""
		switch {
		case !exists:
			action = "created"
		case sha(cur) == newSha:
			action = "kept"
		case prev[r.g.Dst] == sha(cur) || o.Force:
			action = "updated" // el archivo era NUESTRO (o --force): se actualiza
		default:
			action = "conflict" // personalizado por el humano: .new al lado
		}
		if o.DryRun {
			rep.add(r.g.Dst, action)
			continue
		}
		switch action {
		case "created", "updated", "kept":
			if action != "kept" {
				if err := writeFile(abs, r.content, r.g.Mode); err != nil {
					return nil, err
				}
			} else if r.g.Mode&0o111 != 0 {
				_ = os.Chmod(abs, r.g.Mode) // bit de ejecución aunque el contenido no cambie
			}
		case "conflict":
			if err := writeFile(abs+".new", r.content, r.g.Mode); err != nil {
				return nil, err
			}
			next[r.g.Dst] = prev[r.g.Dst] // el canon sigue siendo lo que el humano tiene
		}
		rep.add(r.g.Dst, action)
	}
	if !o.DryRun {
		b, _ := json.MarshalIndent(next, "", "  ")
		if err := writeFile(genManifestPath(o.WS), append(b, '\n'), 0o644); err != nil {
			return nil, err
		}
		registerInstance(o.WS)
	}
	return rep, nil
}

func (r *Report) add(path, action string) {
	r.Files = append(r.Files, FileResult{Path: path, Action: action})
	switch action {
	case "created":
		r.Created++
	case "updated":
		r.Updated++
	case "kept":
		r.Kept++
	case "conflict":
		r.Conflicts++
	}
}

func writeFile(abs string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(abs, content, mode); err != nil {
		return err
	}
	return os.Chmod(abs, mode) // WriteFile no baja/sube bits si el archivo existía
}

// registerInstance — ConfigDir()/instances.json: la lista que `harness update`
// recorre para ofrecer `generate --update` por instancia.
func registerInstance(ws string) {
	path := filepath.Join(ident.ConfigDir(), "instances.json")
	var list []string
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	for _, w := range list {
		if w == ws {
			return
		}
	}
	list = append(list, ws)
	sort.Strings(list)
	b, _ := json.MarshalIndent(list, "", "  ")
	_ = os.MkdirAll(ident.ConfigDir(), 0o700)
	_ = os.WriteFile(path, append(b, '\n'), 0o600)
}
