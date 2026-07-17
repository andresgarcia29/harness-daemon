package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andresgarcia29/harness-daemon/internal/store"
)

func newOpT(t *testing.T) (*Op, string) {
	t.Helper()
	ws := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "op.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewOp(ws, "ws-test", st.DB, 7718), ws
}

func post(t *testing.T, h http.HandlerFunc, token string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "http://127.0.0.1:7718/x", bytes.NewReader(b))
	if token != "" {
		req.Header.Set("X-Corvux-Token", token)
	}
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

// waitFile pollea hasta que el archivo tenga contenido (el launch es async —
// la misma carrera que nos mordió en los tests del panel de Python).
func waitFile(t *testing.T, path string) string {
	t.Helper()
	for i := 0; i < 40; i++ {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

func stubClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "stub.log")
	sh := filepath.Join(dir, "claude-stub")
	os.WriteFile(sh, []byte("#!/bin/sh\necho \"$@\" >> "+log+"\n"), 0o755)
	t.Setenv("HARNESS_CLAUDE_BIN", sh)
	return log
}

func TestGuardSinToken403(t *testing.T) {
	op, _ := newOpT(t)
	rr := post(t, op.OpTask, "", map[string]any{"title": "x"})
	if rr.Code != 403 {
		t.Fatalf("code = %d, quiero 403", rr.Code)
	}
}

func TestGuardHostRaro403(t *testing.T) {
	op, _ := newOpT(t)
	req := httptest.NewRequest("POST", "http://evil.example.com/x", strings.NewReader("{}"))
	req.Header.Set("X-Corvux-Token", op.Token)
	req.Host = "evil.example.com"
	rr := httptest.NewRecorder()
	op.OpTask(rr, req)
	if rr.Code != 403 {
		t.Fatalf("code = %d, quiero 403 (DNS rebinding)", rr.Code)
	}
}

func TestOpTaskCompleto(t *testing.T) {
	op, ws := newOpT(t)
	log := stubClaude(t)
	rr := post(t, op.OpTask, op.Token, map[string]any{
		"title": "Rate limiting por tenant", "context": "100 req/min",
		"priority": "P1", "max_parallel": float64(99), "review_before_ship": true,
		"model": "claude-sonnet-5"})
	if rr.Code != 200 {
		t.Fatalf("code = %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		OK      bool   `json:"ok"`
		ID      string `json:"id"`
		Session string `json:"session"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.OK || !strings.HasPrefix(resp.ID, "AUTO-") {
		t.Fatalf("resp: %+v", resp)
	}
	md, err := os.ReadFile(filepath.Join(ws, "tasks", resp.ID, "task.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"source: panel", "priority: P1", "max_parallel: 12",
		"review_before_ship: true", "preferred_model: claude-sonnet-5", "100 req/min"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("task.md sin %q", want)
		}
	}
	stub := waitFile(t, log)
	if !strings.Contains(stub, "/auto "+resp.ID) ||
		!strings.Contains(stub, "--session-id "+resp.Session) {
		t.Fatalf("el claude stub no recibió los args: %s", stub)
	}
	// el bus registró el intake
	bus, _ := os.ReadFile(filepath.Join(ws, ".harness", "events.jsonl"))
	if !strings.Contains(string(bus), `"kind":"phase"`) {
		t.Fatal("sin evento phase en el bus")
	}
}

func TestOpRespondResume(t *testing.T) {
	op, _ := newOpT(t)
	log := stubClaude(t)
	rr := post(t, op.OpRespond, op.Token, map[string]any{
		"session": "abc-123", "text": "usa Redis"})
	if rr.Code != 200 {
		t.Fatalf("code = %d: %s", rr.Code, rr.Body.String())
	}
	stub := waitFile(t, log)
	if !strings.Contains(stub, "--resume abc-123") {
		t.Fatalf("sin --resume: %s", stub)
	}
}

func TestPaneSendValida(t *testing.T) {
	op, _ := newOpT(t)
	// sin pane/text → 400 (no llega ni a herdr)
	rr := post(t, op.OpPaneSend, op.Token, map[string]any{"pane": "", "text": ""})
	if rr.Code != 400 {
		t.Fatalf("code = %d, quiero 400", rr.Code)
	}
	// texto gigante → 400
	rr = post(t, op.OpPaneSend, op.Token, map[string]any{
		"pane": "w1:p1", "text": strings.Repeat("a", 5000)})
	if rr.Code != 400 {
		t.Fatalf("code = %d, quiero 400 (límite de tamaño)", rr.Code)
	}
}

func TestOpConnectValidaAntesDeGuardar(t *testing.T) {
	op, _ := newOpT(t)
	cfg := t.TempDir()
	t.Setenv("HARNESS_CONFIG_DIR", cfg)
	// proveedor que rechaza → 400 y NADA guardado
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer bad.Close()
	oldL := LinearURL
	LinearURL = bad.URL
	defer func() { LinearURL = oldL }()
	rr := post(t, op.OpConnect, op.Token, map[string]any{"provider": "linear", "token": "lin_api_FAKE"})
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "401") {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(cfg, "linear-token")); !os.IsNotExist(err) {
		t.Fatal("guardó un token inválido")
	}
	// proveedor que acepta → guardado 0600 y connections() true
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer good.Close()
	oldO := OpenRouterKey
	OpenRouterKey = good.URL
	defer func() { OpenRouterKey = oldO }()
	rr = post(t, op.OpConnect, op.Token, map[string]any{"provider": "openrouter", "token": "sk-or-x"})
	if rr.Code != 200 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	fi, err := os.Stat(filepath.Join(cfg, "openrouter-token"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token mal guardado: %v %v", err, fi)
	}
	if !Connections()["openrouter"] {
		t.Fatal("connections no refleja el token")
	}
}

func TestSyncPreciosDesdeOpenRouter(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	must(t, st.SeedBuiltinPrices())
	must(t, st.UpsertMachine("m", "h", "d", "a", "l", 1))
	must(t, st.UpsertWorkspace("w", "", "n", 1))
	must(t, st.UpsertSession("s1", "m", "w", "cli", 1, 1))
	must(t, st.UpsertAgent("s1", "main", "", "", "", 0, 1, 1))
	// un modelo observado SIN precio y uno inexistente
	must(t, st.UpsertCall(store.Call{MessageID: "a", SessionID: "s1", AgentID: "main",
		Model: "glm-4.7", In: 1000, TS: 1}))
	must(t, st.UpsertCall(store.Call{MessageID: "b", SessionID: "s1", AgentID: "main",
		Model: "modelo-fantasma", In: 1000, TS: 1}))
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": "z-ai/glm-4.7", "pricing": map[string]string{"prompt": "0.0000006", "completion": "0.0000022"}},
		}})
	}))
	defer fake.Close()
	old := OpenRouterMod
	OpenRouterMod = fake.URL
	defer func() { OpenRouterMod = old }()
	t.Setenv("HARNESS_CONFIG_DIR", t.TempDir())
	added, missing, err := SyncPrices(st.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "glm-4.7" {
		t.Fatalf("added = %v", added)
	}
	if len(missing) != 1 || missing[0] != "modelo-fantasma" {
		t.Fatalf("missing = %v (lo sin match SE DICE, no se inventa)", missing)
	}
	var in float64
	must(t, st.DB.QueryRow(`SELECT input FROM prices WHERE model='glm-4.7'`).Scan(&in))
	if in < 0.59 || in > 0.61 {
		t.Fatalf("precio glm input = %v, quiero ~0.6/M", in)
	}
	// segunda pasada: ya nada sin precio
	added2, _, _ := SyncPrices(st.DB)
	if len(added2) != 0 {
		t.Fatalf("segunda pasada agregó: %v", added2)
	}
}

func TestOpHerdrValida(t *testing.T) {
	op, _ := newOpT(t)
	// sin id → 400
	rr := post(t, op.OpHerdr, op.Token, map[string]any{"action": "close-pane"})
	if rr.Code != 400 {
		t.Fatalf("sin id: code = %d, quiero 400", rr.Code)
	}
	// id inexistente: 400 si herdr no corre, 404 si corre pero el pane no está.
	// Ambos son rechazos honestos — jamás ejecuta sobre un id inventado.
	rr = post(t, op.OpHerdr, op.Token, map[string]any{"action": "close-pane", "id": "w9:p999"})
	if rr.Code != 400 && rr.Code != 404 {
		t.Fatalf("id inválido: code = %d, quiero 400 o 404", rr.Code)
	}
	// acción desconocida → 400 (sin id se corta antes; con id llega al switch)
	rr = post(t, op.OpHerdr, op.Token, map[string]any{"action": "rm-rf", "id": "x"})
	if rr.Code != 400 {
		t.Fatalf("acción rara: code = %d, quiero 400", rr.Code)
	}
	// sin token → 403 (guard primero)
	rr = post(t, op.OpHerdr, "", map[string]any{"action": "close-pane", "id": "w1:p1"})
	if rr.Code != 403 {
		t.Fatalf("sin token: code = %d, quiero 403", rr.Code)
	}
}

func TestOpHerdrKeyFiltra(t *testing.T) {
	op, _ := newOpT(t)
	// sin teclas válidas → 400 (la letra "rm" no pasa el filtro)
	rr := post(t, op.OpHerdrKey, op.Token, map[string]any{"pane": "w1:p1", "keys": []any{"rm", "sudo"}})
	if rr.Code != 400 {
		t.Fatalf("teclas peligrosas: code = %d, quiero 400", rr.Code)
	}
	// sin pane → 400
	rr = post(t, op.OpHerdrKey, op.Token, map[string]any{"keys": []any{"1"}})
	if rr.Code != 400 {
		t.Fatalf("sin pane: code = %d", rr.Code)
	}
	// sin token → 403
	rr = post(t, op.OpHerdrKey, "", map[string]any{"pane": "w1:p1", "keys": []any{"1"}})
	if rr.Code != 403 {
		t.Fatalf("sin token: %d", rr.Code)
	}
}

func TestHerdrKeyOK(t *testing.T) {
	for _, k := range []string{"0", "5", "9", "y", "n", "Enter", "Up", "Escape"} {
		if !herdrKeyOK(k) {
			t.Errorf("%q debería ser válida", k)
		}
	}
	for _, k := range []string{"rm", "sudo", "10", ";", "a", "$(x)"} {
		if herdrKeyOK(k) {
			t.Errorf("%q NO debería pasar el filtro", k)
		}
	}
}

func TestSafePathYLabel(t *testing.T) {
	if safePath("relativo") != "" || safePath("/etc/../x") != "" {
		t.Fatal("safePath debe rechazar relativas y ..")
	}
	if safePath("/tmp/x") != "/tmp/x" {
		t.Fatal("safePath debe aceptar absolutas limpias")
	}
	if labelOK("hola; rm -rf /") != "hola rm -rf " {
		t.Fatalf("labelOK dejó pasar shell: %q", labelOK("hola; rm -rf /"))
	}
}

func TestOpHerdrStopSession(t *testing.T) {
	op, _ := newOpT(t)
	// stop-session con id → válido en el switch; sin herdr corriendo el test da 400
	rr := post(t, op.OpHerdr, op.Token, map[string]any{"action": "stop-session", "id": "default"})
	if rr.Code != 400 && rr.Code != 200 && rr.Code != 500 {
		t.Fatalf("stop-session: code inesperado %d", rr.Code)
	}
	// sin token → 403
	rr = post(t, op.OpHerdr, "", map[string]any{"action": "stop-session", "id": "default"})
	if rr.Code != 403 {
		t.Fatalf("sin token: %d", rr.Code)
	}
}
