package gen

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseAnswersYAML — parser FIJO del esquema de harness-answers.yaml (el
// mismo contrato que doctor.sh parsea con grep/awk). No es un parser YAML
// general a propósito: cero dependencias, y si el esquema cambió, esto DEBE
// romper ruidosamente en vez de adivinar. Tolerante a comentarios y quotes.
func ParseAnswersYAML(b []byte) (*Answers, error) {
	a := &Answers{}
	var section, sub string // sección top-level y contexto de lista
	var curCluster *Cluster
	var curCap *CapSel

	flushCluster := func() {
		if curCluster != nil {
			a.Clusters = append(a.Clusters, *curCluster)
			curCluster = nil
		}
	}
	flushCap := func() {
		if curCap != nil {
			a.Capabilities = append(a.Capabilities, *curCap)
			curCap = nil
		}
	}

	for n, raw := range strings.Split(string(b), "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		t := strings.TrimSpace(line)

		if indent == 0 { // nueva sección top-level
			flushCluster()
			flushCap()
			key, val, _ := strings.Cut(t, ":")
			key = strings.TrimSpace(key)
			val = clean(val)
			section, sub = key, ""
			switch key {
			case "flow":
				a.Flow = val
			case "autonomy":
				a.Autonomy = val
			case "minion_decompose":
				a.MinionDecompose = (val == "true")
			case "loop_budget":
				if v, err := strconv.Atoi(val); err == nil {
					a.LoopBudget = v
				}
			case "harness_version", "generated_at", "project", "instance", "models",
				"dag", "clusters", "capabilities", "secrets", "tickets", "memory":
				// secciones/metadata conocidas
			default:
				return nil, fmt.Errorf("línea %d: clave desconocida %q — el esquema fijo cambió", n+1, key)
			}
			continue
		}

		switch section {
		case "project":
			setKV(t, map[string]*string{"name": &a.Project.Name, "ticket_prefix": &a.Project.TicketPrefix})
		case "instance":
			setKV(t, map[string]*string{"repo": &a.Instance.Repo})
		case "models":
			setKV(t, map[string]*string{
				"provider":  &a.Models.Provider,
				"architect": &a.Models.Architect, "reviewer": &a.Models.Reviewer,
				"implementer": &a.Models.Implementer, "mechanical": &a.Models.Mechanical,
			})
		case "tickets":
			setKV(t, map[string]*string{"provider": &a.Tickets.Provider})
		case "memory":
			if strings.HasPrefix(t, "profiles:") {
				a.Memory.Profiles = inlineList(t)
			} else {
				setKV(t, map[string]*string{"provider": &a.Memory.Provider})
			}
		case "secrets":
			switch {
			case strings.HasPrefix(t, "source:"):
				a.Secrets.Source = clean(strings.TrimPrefix(t, "source:"))
			case strings.HasPrefix(t, "refs:"):
				sub = "refs"
			case sub == "refs" && strings.HasPrefix(t, "- "):
				a.Secrets.Refs = append(a.Secrets.Refs, clean(strings.TrimPrefix(t, "- ")))
			case sub == "refs" && t == "[]":
				// lista vacía
			}
		case "dag":
			if strings.HasPrefix(t, "- ") {
				a.DAG = append(a.DAG, clean(strings.TrimPrefix(t, "- ")))
			}
		case "clusters":
			if t == "[]" {
				continue
			}
			if strings.HasPrefix(t, "- ") {
				flushCluster()
				curCluster = &Cluster{}
				t = strings.TrimPrefix(t, "- ")
			}
			if curCluster == nil {
				continue
			}
			key, val, _ := strings.Cut(t, ":")
			switch strings.TrimSpace(key) {
			case "agent":
				curCluster.Agent = clean(val)
			case "kind":
				curCluster.Kind = clean(val)
			case "owns":
				curCluster.Owns = clean(val)
			case "repos":
				curCluster.Repos = parseInline(val)
			}
		case "capabilities":
			if t == "[]" {
				continue
			}
			if strings.HasPrefix(t, "- ") {
				flushCap()
				curCap = &CapSel{}
				t = strings.TrimPrefix(t, "- ")
			}
			if curCap == nil {
				continue
			}
			key, val, _ := strings.Cut(t, ":")
			switch strings.TrimSpace(key) {
			case "name":
				curCap.Name = clean(val)
			case "bin":
				curCap.Bin = clean(val)
			case "mcp":
				curCap.Mcp = clean(val)
			case "tier":
				curCap.Tier = clean(val)
			case "scope":
				curCap.Scope = clean(val)
			case "profiles":
				curCap.Profiles = parseInline(val)
			}
		}
	}
	flushCluster()
	flushCap()
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return a, nil
}

func stripComment(line string) string {
	// corta " #" fuera de comillas (suficiente para este esquema)
	inQ := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQ = !inQ
		case '#':
			if !inQ && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
				return line[:i]
			}
		}
	}
	return line
}

func clean(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, `"'`)
	return v
}

func setKV(t string, dst map[string]*string) {
	key, val, ok := strings.Cut(t, ":")
	if !ok {
		return
	}
	if p, ok := dst[strings.TrimSpace(key)]; ok {
		*p = clean(val)
	}
}

func parseInline(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if s := clean(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func inlineList(t string) []string {
	_, val, _ := strings.Cut(t, ":")
	return parseInline(val)
}
