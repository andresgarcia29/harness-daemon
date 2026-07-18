package api

import "testing"

func TestValidTargetSSHFormas(t *testing.T) {
	good := []string{"corvux", "andres@10.0.0.9", "andres@vps.midominio.com", "ssh://andres@1.2.3.4:2222", "ssh://root@host"}
	bad := []string{"", "-oProxyCommand=evil", "user@host:2222", "ssh://x:99999x", "a b"}
	for _, g := range good {
		if !validTargetSSH(g) {
			t.Errorf("debió aceptar %q", g)
		}
	}
	for _, b := range bad {
		if validTargetSSH(b) {
			t.Errorf("debió rechazar %q", b)
		}
	}
}
