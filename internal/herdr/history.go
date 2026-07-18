package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/redact"
)

// ═══ Ruta B: backlog acumulado de pantalla (para shells sin agente) ═══
//
// herdr sólo expone la pantalla VISIBLE (no el scrollback, verificado en su
// doc). Para "tener todo" de un shell acumulamos cada captura en un buffer
// propio: separamos la pantalla ACTUAL (se reemplaza cada poll) de lo ya
// COMMITEADO (líneas que scrollearon hacia arriba). Así el pie/prompt que
// cambia cada frame NO ensucia el historial — sólo se comitea lo que sube.

type paneHist struct {
	committed []string
	screen    []string
	at        time.Time
}

var (
	histMu   sync.Mutex
	histBufs = map[string]*paneHist{}
)

const histMaxLines = 5000

func histKey(target, pane string) string { return target + "\x00" + pane }

// AccumulateVisible mezcla una captura de pantalla nueva al backlog del pane.
func AccumulateVisible(target, pane, text string) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1] // sin colas en blanco
	}
	histMu.Lock()
	defer histMu.Unlock()
	h := histBufs[histKey(target, pane)]
	if h == nil {
		h = &paneHist{}
		histBufs[histKey(target, pane)] = h
	}
	h.at = time.Now()
	mergeScreen(h, lines)
	if over := len(h.committed) - histMaxLines; over > 0 {
		h.committed = h.committed[over:]
	}
}

// mergeScreen decide cuánto de la pantalla vieja "scrolleó" (subió) y lo comitea;
// el resto es la pantalla viva y se reemplaza. Alinea la pantalla vieja
// desplazada k líneas contra la nueva; el mejor solape indica el scroll.
func mergeScreen(h *paneHist, next []string) {
	old := h.screen
	if len(old) == 0 {
		h.screen = next
		return
	}
	// para cada desplazamiento k, cuenta cuántas líneas coinciden alineando la
	// pantalla vieja (desde k) con la nueva (desde 0). El k con más coincidencias
	// es el scroll más probable. (Contar el prefijo — no exigir match total —
	// tolera que el pie/prompt cambie en la última línea.)
	bestK, bestOv := 0, 0
	for k := 0; k < len(old); k++ {
		ov := 0
		for k+ov < len(old) && ov < len(next) && old[k+ov] == next[ov] {
			ov++
		}
		if ov > bestOv {
			bestOv, bestK = ov, k
		}
	}
	if bestOv >= len(old)/2 && bestOv > 0 {
		h.committed = append(h.committed, old[:bestK]...) // sólo lo que subió
	} else {
		h.committed = append(h.committed, old...) // pantalla nueva: conserva la vieja entera
	}
	h.screen = next
}

// Backlog devuelve el historial acumulado (comiteado + pantalla viva).
func Backlog(target, pane string) string {
	histMu.Lock()
	defer histMu.Unlock()
	h := histBufs[histKey(target, pane)]
	if h == nil {
		return ""
	}
	out := make([]string, 0, len(h.committed)+len(h.screen))
	out = append(out, h.committed...)
	out = append(out, h.screen...)
	return strings.Join(out, "\n")
}

// ═══ Ruta A: transcripción real del agente (Claude Code) por session id ═══

var sidOK = regexp.MustCompile(`^[A-Za-z0-9._-]{6,80}$`)

// Transcript lee el JSONL de la sesión de un agente y devuelve la conversación
// legible (roles + texto), redactada. Local: glob en ~/.claude/projects.
// Remoto: por SSH. Es la fuente COMPLETA y limpia — no la pantalla.
func (c Client) Transcript(sessionID string) (string, error) {
	if !sidOK.MatchString(sessionID) {
		return "", nil
	}
	raw, err := c.readTranscriptFile(sessionID)
	if err != nil || raw == "" {
		return "", err
	}
	return redact.String(renderTranscript(raw)), nil
}

func (c Client) readTranscriptFile(sid string) (string, error) {
	if c.target == "" {
		home, _ := os.UserHomeDir()
		for _, root := range []string{filepath.Join(home, ".claude", "projects"), filepath.Join(home, ".config", "claude", "projects")} {
			hits, _ := filepath.Glob(filepath.Join(root, "*", sid+".jsonl"))
			if len(hits) > 0 {
				b, err := os.ReadFile(hits[0])
				return string(b), err
			}
		}
		return "", nil
	}
	// remoto: el sid está validado (UUID-ish); el glob lo expande el shell remoto
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	remote := remotePATH + "sh -c 'cat \"$HOME/.claude/projects/\"*/" + sid + ".jsonl \"$HOME/.config/claude/projects/\"*/" + sid + ".jsonl 2>/dev/null | head -c 4000000'"
	out, err := exec.CommandContext(ctx, "ssh", append(sshBase(), c.target, remote)...).Output()
	return string(out), err
}

// renderTranscript convierte el JSONL de Claude Code en texto legible: cada
// mensaje de user/assistant con su texto; las tool-calls se resumen en una línea.
func renderTranscript(raw string) string {
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		role := rec.Message.Role
		if role == "" {
			role = rec.Type
		}
		if role != "user" && role != "assistant" {
			continue
		}
		text := extractText(rec.Message.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		label := "▸ tú"
		if role == "assistant" {
			label = "● claude"
		}
		b.WriteString(label + "\n")
		b.WriteString(strings.TrimRight(text, "\n"))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// extractText saca el texto de un content de Claude Code: string directo, o un
// array de bloques (text / tool_use / tool_result / thinking).
func extractText(c json.RawMessage) string {
	if len(c) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(c, &s) == nil {
		return s
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(c, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, bl := range blocks {
		switch bl.Type {
		case "text":
			if bl.Text != "" {
				parts = append(parts, bl.Text)
			}
		case "thinking":
			// el razonamiento no lo mostramos como texto principal
		case "tool_use":
			parts = append(parts, "  ⚙ "+bl.Name)
		case "tool_result":
			// resultados de tool: ruido para una lectura; se omiten
		}
	}
	return strings.Join(parts, "\n")
}
