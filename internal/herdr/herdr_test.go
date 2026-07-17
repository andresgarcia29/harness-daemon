package herdr

import "testing"

// JSON real capturado de herdr 0.7.3 (recortado).
const realSnap = `{"id":"cli:api:snapshot","result":{"snapshot":{"version":"0.7.3","protocol":16,` +
	`"workspaces":[{"workspace_id":"w8","label":"corvux","number":1,"agent_status":"working","pane_count":2,"tab_count":2,"focused":true},` +
	`{"workspace_id":"w9","label":"latam","number":2,"agent_status":"idle","pane_count":1,"tab_count":1,"focused":false}],` +
	`"tabs":[{"tab_id":"w8:t2","workspace_id":"w8","label":"Harness","agent_status":"working","pane_count":1}],` +
	`"panes":[{"pane_id":"w8:p2","workspace_id":"w8","tab_id":"w8:t2","cwd":"/x","agent_status":"working","focused":false}],` +
	`"agents":[{"name":"demo","pane_id":"w8:p2","workspace_id":"w8","agent_status":"working","cwd":"/x"}]}}}`

func TestParseSnapshotReal(t *testing.T) {
	st := parse([]byte(realSnap))
	if !st.Available || st.Version != "0.7.3" {
		t.Fatalf("available/version: %v %q", st.Available, st.Version)
	}
	if len(st.Workspaces) != 2 || st.Workspaces[0].Label != "corvux" || st.Workspaces[0].AgentStatus != "working" {
		t.Fatalf("workspaces mal: %+v", st.Workspaces)
	}
	if len(st.Panes) != 1 || st.Panes[0].PaneID != "w8:p2" {
		t.Fatalf("panes mal: %+v", st.Panes)
	}
	if len(st.Agents) != 1 || st.Agents[0].Name != "demo" {
		t.Fatalf("agents mal: %+v", st.Agents)
	}
}

func TestParseBasura(t *testing.T) {
	st := parse([]byte("no soy json"))
	if st.Available {
		t.Fatal("basura no debe reportar available")
	}
	if st.Panes == nil || st.Workspaces == nil {
		t.Fatal("los slices deben ser no-nil aunque falle (para el JSON del API)")
	}
}

func TestItoa(t *testing.T) {
	for _, c := range []struct {
		n int
		s string
	}{{0, "0"}, {60, "60"}, {200, "200"}} {
		if got := itoa(c.n); got != c.s {
			t.Errorf("itoa(%d)=%q", c.n, got)
		}
	}
}
