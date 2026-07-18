package gen

import "testing"

func TestCatalogEmbebido(t *testing.T) {
	caps, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) < 50 {
		t.Fatalf("el catálogo trae ~58 capacidades, leí %d", len(caps))
	}
	jq, ok := CapByName("jq")
	if !ok || jq.Provider != "cli" || jq.Bin != "jq" || jq.Detect != "always" {
		t.Fatalf("jq: %+v", jq)
	}
	mcps := 0
	for _, c := range caps {
		if c.Provider == "mcp" {
			mcps++
			if c.Mcp == "" || c.Config == nil || c.Config.Command == "" {
				t.Fatalf("MCP sin config: %+v", c)
			}
		}
	}
	if mcps < 10 {
		t.Fatalf("faltan MCPs en el catálogo: %d", mcps)
	}
}

func TestManifestEmbebido(t *testing.T) {
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.InstallerCommit == "" || len(m.Files) < 50 {
		t.Fatalf("manifest raro: commit=%q files=%d", m.InstallerCommit, len(m.Files))
	}
	if _, ok := m.Files["scripts/discover.sh"]; !ok {
		t.Fatal("discover.sh debe estar embebido y hasheado")
	}
}
