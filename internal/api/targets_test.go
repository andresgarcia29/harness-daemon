package api

import "testing"

func TestValidTargetSSH(t *testing.T) {
	ok := []string{"vps", "user@host", "my-alias", "host.example.com", "deploy@10.0.0.5", "a_b-c.d"}
	for _, s := range ok {
		if !validTargetSSH(s) {
			t.Errorf("%q debería ser un destino SSH válido", s)
		}
	}
	// Peligrosos: guion inicial = inyección de opciones de ssh (-oProxyCommand),
	// espacios/metacaracteres = intento de shell.
	bad := []string{"", "-oProxyCommand=x", "-t", "a b", "a;b", "$(whoami)", "a|b", "a&b", "a`b`", "host/../x", "muy" + string(make([]byte, 200))}
	for _, s := range bad {
		if validTargetSSH(s) {
			t.Errorf("%q NO debería pasar como destino SSH", s)
		}
	}
}

func TestValidTargetName(t *testing.T) {
	if !validTargetName("vps prod-1.eu") {
		t.Error("nombre razonable rechazado")
	}
	for _, n := range []string{"", "a;rm", "x$(y)", "a/b"} {
		if validTargetName(n) {
			t.Errorf("%q NO debería ser un nombre válido", n)
		}
	}
}

func TestResolveTargetLocal(t *testing.T) {
	if ssh, ok := ResolveTarget(""); !ok || ssh != "" {
		t.Error("target vacío debe resolver a local ('', true)")
	}
	if ssh, ok := ResolveTarget("local"); !ok || ssh != "" {
		t.Error("'local' debe resolver a ('', true)")
	}
	// un nombre que casi seguro no existe → rechazado (el caller no ejecuta nada)
	if _, ok := ResolveTarget("no-existe-zzz-9271"); ok {
		t.Error("un target desconocido debe devolver ok=false")
	}
}
