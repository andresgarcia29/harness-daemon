package redact

import (
	"strings"
	"testing"
)

func TestRedactaFamilias(t *testing.T) {
	in := "ghp_0123456789012345678901234567 sk-abcdefghijklmnopqrstuvwx hvs.AbCdEfGhIjKlMnOpQrStUv lin_api_ABCDEFGHIJ0123456789xx"
	out := String(in)
	for _, leak := range []string{"ghp_012345", "sk-abcdefghij", "hvs.AbCdEf", "lin_api_ABCD"} {
		if strings.Contains(out, leak) {
			t.Errorf("se coló %q en %q", leak, out)
		}
	}
	if !strings.Contains(out, "REDACTADO") {
		t.Error("no marcó la redacción")
	}
}

func TestClipRecorta(t *testing.T) {
	if got := Clip("hola mundo", 4); got != "hola […]" {
		t.Errorf("Clip = %q", got)
	}
	if String("") != "" {
		t.Error("String('') debe ser ''")
	}
}
