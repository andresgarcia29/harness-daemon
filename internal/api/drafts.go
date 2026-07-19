package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/gen"
	"github.com/andresgarcia29/harness-daemon/internal/herdr"
)

// ── los DRAFT: la ley que la arqueología propuso y nadie ha firmado ──
// La constitución de un abogado en DRAFT es la primera parada de /auto:
// litigar citando ley sin firmar es teatro. Este plano lista los borradores
// y deja RATIFICARLOS desde el panel — el acto humano que los vuelve ley.

type DraftDoc struct {
	Path  string `json:"path"`  // relativo al workspace
	Kind  string `json:"kind"`  // abogado | constitution | spec | map
	Title string `json:"title"` // legible
}

// draftGlobs — dónde puede vivir un DRAFT (lista cerrada: jamás ship.sh,
// hooks ni settings — la ley del operador de ADR-0010 sigue intacta).
func draftCandidates(ws string) []DraftDoc {
	var out []DraftDoc
	add := func(rel, kind, title string) {
		out = append(out, DraftDoc{Path: rel, Kind: kind, Title: title})
	}
	if m, _ := filepath.Glob(filepath.Join(ws, ".claude", "agents", "*.md")); m != nil {
		for _, p := range m {
			name := strings.TrimSuffix(filepath.Base(p), ".md")
			add(filepath.Join(".claude", "agents", filepath.Base(p)), "abogado", "Constitución de "+name)
		}
	}
	add(filepath.Join("docs", "constitution.md"), "constitution", "Constitución del proyecto")
	add(filepath.Join("docs", "architecture", "map.md"), "map", "Mapa de ownership")
	if m, _ := filepath.Glob(filepath.Join(ws, "specs", "*", "spec.md")); m != nil {
		for _, p := range m {
			dom := filepath.Base(filepath.Dir(p))
			add(filepath.Join("specs", dom, "spec.md"), "spec", "Spec de "+dom)
		}
	}
	return out
}

// ListDrafts — los documentos que HOY están en status: DRAFT.
func ListDrafts(ws string) []DraftDoc {
	if ws == "" {
		return nil
	}
	var out []DraftDoc
	for _, c := range draftCandidates(ws) {
		if isDraft(filepath.Join(ws, c.Path)) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func isDraft(abs string) bool {
	b, err := os.ReadFile(abs)
	if err != nil {
		return false
	}
	head := b
	if len(head) > 2048 {
		head = head[:2048]
	}
	return strings.Contains(string(head), "status: DRAFT")
}

// OpRatify — ratifica UN draft ({path}) o todos ({all:true}). Solo archivos
// de la lista cerrada de candidatos y solo el flip DRAFT→RATIFIED: el
// operador firma la ley, jamás la reescribe (ADR-0010 intacto).
func (o *Op) OpRatify(rw http.ResponseWriter, r *http.Request) {
	b, ok := o.Guard(rw, r)
	if !ok {
		return
	}
	// mirando un VPS (el op() del panel inyecta target): la firma corre ALLÁ
	// por ssh — mismo código (harness ratify), misma ley
	if tname := s(b, "target"); tname != "" {
		tgt, okk := ResolveTargetFull(tname)
		if !okk || tgt.SSH == "" {
			fail(rw, 400, "target desconocido")
			return
		}
		if tgt.Path == "" {
			fail(rw, 400, "el target no tiene ruta de workspace configurada")
			return
		}
		args := []string{"harness", "ratify", "--workspace", tgt.Path, "--json"}
		if v, _ := b["all"].(bool); v {
			args = append(args, "--all")
		} else {
			p := s(b, "path")
			if p == "" {
				fail(rw, 400, "falta path o all")
				return
			}
			args = append(args, "--path", p)
		}
		out, err := herdr.Exec(tgt.SSH, args, nil, 60*time.Second, nil)
		var res map[string]any
		if json.Unmarshal(out, &res) != nil {
			if err != nil {
				fail(rw, 502, "VPS: "+err.Error())
				return
			}
			fail(rw, 502, "respuesta rara del VPS")
			return
		}
		writeJSON(rw, 200, res)
		return
	}
	if o.WS == "" {
		fail(rw, 409, "workspace no fijado")
		return
	}
	var done, failed []string
	if v, _ := b["all"].(bool); v {
		for _, d := range ListDrafts(o.WS) {
			if err := RatifyDoc(o.WS, d.Path); err != nil {
				failed = append(failed, d.Path+": "+err.Error())
			} else {
				done = append(done, d.Path)
			}
		}
	} else {
		p := s(b, "path")
		if err := RatifyDoc(o.WS, p); err != nil {
			fail(rw, 400, err.Error())
			return
		}
		done = append(done, p)
	}
	for _, p := range done {
		o.emit("decision", "el humano ratificó "+p, "")
	}
	writeJSON(rw, 200, map[string]any{"ok": len(failed) == 0, "ratified": done, "failed": failed,
		"drafts": ListDrafts(o.WS)})
}

func RatifyDoc(ws, rel string) error {
	found := false
	for _, c := range draftCandidates(ws) {
		if c.Path == rel {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("ese archivo no es un documento ratificable")
	}
	abs := filepath.Join(ws, rel)
	b, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), "status: DRAFT") {
		return fmt.Errorf("%s ya no está en DRAFT", rel)
	}
	// la LÍNEA entera se reemplaza: dejar el comentario viejo al lado
	// ("# ⚠️ RATIFICAR POR HUMANO…") junto a RATIFIED confunde a cualquier
	// agente que lo lea — la firma debe verse firmada
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if idx := strings.Index(l, "status: DRAFT"); idx >= 0 {
			lines[i] = l[:idx] + "status: RATIFIED  # firmado por humano desde el panel"
			break
		}
	}
	out := strings.Join(lines, "\n")
	fi, _ := os.Stat(abs)
	mode := os.FileMode(0o644)
	if fi != nil {
		mode = fi.Mode()
	}
	if err := os.WriteFile(abs, []byte(out), mode); err != nil {
		return err
	}
	// firmar es una transformación SANCIONADA: el doc sigue siendo "nuestro"
	// para generate --update (no un .new de conflicto)
	gen.UpdateManifestSha(ws, rel, []byte(out))
	return nil
}

// ReadDoc — el visor: leer un documento de la ley para REVISARLO antes de
// firmar (la queja real: "la UI no me dejaba verlos"). Solo .md bajo docs/,
// specs/ o .claude/agents/ — lectura, jamás escritura.
func ReadDoc(ws, rel string) (string, error) {
	rel = filepath.Clean(rel)
	if strings.Contains(rel, "..") || filepath.IsAbs(rel) || !strings.HasSuffix(rel, ".md") {
		return "", fmt.Errorf("ruta inválida")
	}
	okPrefix := false
	for _, p := range []string{"docs/", "specs/", filepath.Join(".claude", "agents") + "/"} {
		if strings.HasPrefix(rel, p) {
			okPrefix = true
		}
	}
	if !okPrefix {
		return "", fmt.Errorf("solo documentos de docs/, specs/ o .claude/agents/")
	}
	b, err := os.ReadFile(filepath.Join(ws, rel))
	if err != nil {
		return "", fmt.Errorf("no pude leer %s", rel)
	}
	if len(b) > 256*1024 {
		b = b[:256*1024]
	}
	return string(b), nil
}

// DocHandler — GET /api/doc?path=…[&target=…]: local del workspace, o del
// VPS por ssh (cat con argv quoteado — mismo código, misma ley).
func DocHandler(ws string) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if !GuardRead(rw, r) {
			return
		}
		rel := r.URL.Query().Get("path")
		rw.Header().Set("Content-Type", "application/json")
		if tname := r.URL.Query().Get("target"); tname != "" {
			tgt, ok := ResolveTargetFull(tname)
			if !ok || tgt.SSH == "" || tgt.Path == "" {
				writeJSON(rw, 400, map[string]any{"ok": false, "error": "target sin workspace"})
				return
			}
			// validación local de la ruta ANTES de tocar el VPS
			if _, err := func() (string, error) {
				rel2 := filepath.Clean(rel)
				if strings.Contains(rel2, "..") || filepath.IsAbs(rel2) || !strings.HasSuffix(rel2, ".md") {
					return "", fmt.Errorf("ruta inválida")
				}
				return rel2, nil
			}(); err != nil {
				writeJSON(rw, 400, map[string]any{"ok": false, "error": err.Error()})
				return
			}
			out, err := herdr.Exec(tgt.SSH, []string{"cat", filepath.Join(tgt.Path, filepath.Clean(rel))}, nil, 20*time.Second, nil)
			if err != nil {
				writeJSON(rw, 502, map[string]any{"ok": false, "error": "VPS: " + err.Error()})
				return
			}
			if len(out) > 256*1024 {
				out = out[:256*1024]
			}
			writeJSON(rw, 200, map[string]any{"ok": true, "path": rel, "content": string(out)})
			return
		}
		if ws == "" {
			writeJSON(rw, 409, map[string]any{"ok": false, "error": "sin workspace local — elige una máquina"})
			return
		}
		content, err := ReadDoc(ws, rel)
		if err != nil {
			writeJSON(rw, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(rw, 200, map[string]any{"ok": true, "path": rel, "content": content})
	}
}
