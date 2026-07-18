package herdr

import (
	"strings"
	"testing"
)

// Simula un shell que scrollea: cada "captura" añade una línea abajo y sube.
func TestMergeScreenScroll(t *testing.T) {
	h := &paneHist{}
	// pantalla de 3 líneas que va scrolleando
	mergeScreen(h, []string{"L1", "L2", "L3"})
	mergeScreen(h, []string{"L2", "L3", "L4"}) // scrolleó 1: comitea L1
	mergeScreen(h, []string{"L3", "L4", "L5"}) // scrolleó 1: comitea L2
	got := strings.Join(append(append([]string{}, h.committed...), h.screen...), "|")
	if got != "L1|L2|L3|L4|L5" {
		t.Fatalf("reconstrucción del scroll mal: %q", got)
	}
}

func TestMergeScreenInPlace(t *testing.T) {
	h := &paneHist{}
	// el pie/prompt cambia pero el contenido NO scrollea → no debe duplicar
	mergeScreen(h, []string{"msg", "msg2", "prompt v1"})
	mergeScreen(h, []string{"msg", "msg2", "prompt v2"})
	mergeScreen(h, []string{"msg", "msg2", "prompt v3"})
	all := append(append([]string{}, h.committed...), h.screen...)
	got := strings.Join(all, "|")
	if got != "msg|msg2|prompt v3" {
		t.Fatalf("cambio en el lugar duplicó historial: %q", got)
	}
}
