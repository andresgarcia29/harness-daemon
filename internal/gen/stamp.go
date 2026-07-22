package gen

import (
	"os"
	"strings"
)

// resolveModelAliases — los templates estampan ALIASES (fast|smart|deep) en el
// frontmatter de los agentes (el valor que viene de answers). Claude Code
// exige IDs reales. La traducción vive en models.yaml (única fuente, mismo
// formato estricto que parsea scripts/stamp-models.sh); aquí se aplica EN
// MEMORIA antes de escribir: el generate nace idempotente y el check de
// stamp-models del doctor queda en verde desde el primer segundo.
func resolveModelAliases(items []rendered) {
	var modelsYaml string
	for _, it := range items {
		if it.g.Dst == "models.yaml" {
			modelsYaml = string(it.content)
			break
		}
	}
	if modelsYaml == "" {
		return
	}
	provider := yamlTopKey(modelsYaml, "provider")
	table := yamlSectionKV(modelsYaml, "models."+provider)
	if len(table) == 0 {
		return
	}
	for i := range items {
		d := items[i].g.Dst
		if !strings.HasPrefix(d, ".claude/agents/") || !strings.HasSuffix(d, ".md") {
			continue
		}
		items[i].content = resolveAgentModel(items[i].content, table)
	}
}

// resolveAgentModel — reemplaza el alias del frontmatter `model:` por el ID
// del proveedor. Lo usan el generate (en memoria) y el restamp de la
// arqueología (que re-renderiza abogados DESPUÉS del generate: sin esto, el
// restamp des-estampaba al abogado y el doctor reportaba drift).
func resolveAgentModel(content []byte, table map[string]string) []byte {
	lines := strings.Split(string(content), "\n")
	for j, ln := range lines {
		if strings.HasPrefix(ln, "model: ") {
			alias := strings.TrimSpace(strings.TrimPrefix(ln, "model: "))
			if id, ok := table[alias]; ok {
				lines[j] = "model: " + id
			}
			break
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// modelTableFromWS — la tabla alias→ID del models.yaml YA escrito en el ws.
func modelTableFromWS(ws string) map[string]string {
	b, err := os.ReadFile(ws + "/models.yaml")
	if err != nil {
		return nil
	}
	doc := string(b)
	return yamlSectionKV(doc, "models."+yamlTopKey(doc, "provider"))
}

// yamlTopKey — escalar de nivel superior del formato estricto de models.yaml.
func yamlTopKey(doc, key string) string {
	for _, ln := range strings.Split(doc, "\n") {
		if strings.HasPrefix(ln, key+":") {
			v := strings.TrimSpace(strings.TrimPrefix(ln, key+":"))
			if i := strings.Index(v, "#"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			return v
		}
	}
	return ""
}

// yamlSectionKV — claves indentadas (2 espacios) bajo una sección a columna 0.
func yamlSectionKV(doc, section string) map[string]string {
	out := map[string]string{}
	in := false
	for _, ln := range strings.Split(doc, "\n") {
		if len(ln) > 0 && ln[0] != ' ' && ln[0] != '#' {
			in = strings.HasPrefix(ln, section+":")
			continue
		}
		if !in || !strings.HasPrefix(ln, "  ") {
			continue
		}
		kv := strings.SplitN(strings.TrimSpace(ln), ":", 2)
		if len(kv) != 2 {
			continue
		}
		v := strings.TrimSpace(kv[1])
		if i := strings.Index(v, "#"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if kv[0] != "" && v != "" {
			out[kv[0]] = v
		}
	}
	return out
}
