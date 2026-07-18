package gen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ArchFinding — lo que la arqueología devuelve por cluster (contrato del
// prompt archaeology-service.md).
type ArchFinding struct {
	Owns       string   `json:"owns"`
	NotOwns    string   `json:"not_owns"`
	Invariants []string `json:"invariants"`
	Requirements []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Ears     string `json:"ears"`
		Scenario string `json:"scenario"`
		Evidence string `json:"evidence"`
	} `json:"requirements"`
}

// Restamp re-estampa la constitución del abogado y la spec del dominio con lo
// que la arqueología encontró — SOLO si los archivos siguen siendo los que el
// generador escribió (gen-manifest): una personalización humana jamás se pisa.
// Todo queda DRAFT: la arqueología propone, el humano ratifica.
func Restamp(ws string, a *Answers, c Cluster, f *ArchFinding, o Opts) error {
	manifest := loadGenManifest(ws)
	base := Vars(a, nil, o)

	// 1) el abogado
	agentDst := ".claude/agents/" + c.Agent + ".md"
	raw, err := Asset("templates/agents/svc-agent.md.tmpl")
	if err != nil {
		return err
	}
	vars := map[string]string{}
	for k, v := range base {
		vars[k] = v
	}
	vars["AGENT_NAME"] = c.Agent
	vars["CLUSTER_LABEL"] = c.Agent
	vars["REPOS_CSV"] = strings.Join(c.Repos, ", ")
	vars["OWNS"] = defaultStr(f.Owns, "_TBD_")
	vars["NOT_OWNS"] = defaultStr(f.NotOwns, "_TBD_")
	vars["INVARIANTS"] = joinOr(f.Invariants, "; ", "_TBD_")
	content, err := Render(agentDst, raw, vars)
	if err != nil {
		return err
	}
	if err := restampWrite(ws, agentDst, content, 0o644, manifest); err != nil {
		return err
	}

	// 2) la spec del dominio (esqueleto + requirements reales de la arqueología)
	dom := strings.TrimPrefix(c.Agent, "svc-")
	specDst := "specs/" + dom + "/spec.md"
	rawSpec, err := Asset("templates/docs/spec.md.tmpl")
	if err != nil {
		return err
	}
	vars["CAPABILITY"] = dom
	vars["OWNER_AGENT"] = c.Agent
	vars["PREFIX"] = strings.ToUpper(dom)
	spec, err := Render(specDst, rawSpec, vars)
	if err != nil {
		return err
	}
	if len(f.Requirements) > 0 {
		var b strings.Builder
		b.WriteString("\n## Requirements propuestos por la arqueología (DRAFT — ratificar)\n\n")
		for _, r := range f.Requirements {
			fmt.Fprintf(&b, "### %s — %s\n\n- EARS: %s\n- Escenario: %s\n- Evidencia: %s\n\n",
				r.ID, r.Title, r.Ears, r.Scenario, r.Evidence)
		}
		spec = append(spec, []byte(b.String())...)
	}
	if err := restampWrite(ws, specDst, spec, 0o644, manifest); err != nil {
		return err
	}

	// persistir el manifest actualizado
	b, _ := json.MarshalIndent(manifest, "", "  ")
	return writeFile(genManifestPath(ws), append(b, '\n'), 0o644)
}

// restampWrite escribe SOLO si el archivo actual es el generado (o no existe).
func restampWrite(ws, dst string, content []byte, mode os.FileMode, manifest map[string]string) error {
	abs := filepath.Join(ws, dst)
	if cur, err := os.ReadFile(abs); err == nil {
		if manifest[dst] != "" && sha(cur) != manifest[dst] {
			return fmt.Errorf("%s fue personalizado — la arqueología no lo pisa (ratifícalo a mano)", dst)
		}
	}
	if err := writeFile(abs, content, mode); err != nil {
		return err
	}
	manifest[dst] = sha(content)
	return nil
}

func joinOr(items []string, sep, def string) string {
	if len(items) == 0 {
		return def
	}
	return strings.Join(items, sep)
}
