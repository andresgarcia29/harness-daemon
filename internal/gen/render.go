package gen

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Render — el motor {{KEY}} estricto y fail-closed: un placeholder sin valor
// es un ERROR con nombre de archivo, no un "{{X}}" silencioso en producción.
// No es text/template a propósito: los templates son del instalador (bash,
// yaml, markdown) y cualquier sintaxis rica chocaría con su contenido.
var phRe = regexp.MustCompile(`\{\{[A-Z_]+\}\}`)

func Render(name string, tmpl []byte, vars map[string]string) ([]byte, error) {
	missing := map[string]bool{}
	out := phRe.ReplaceAllFunc(tmpl, func(m []byte) []byte {
		key := string(m[2 : len(m)-2])
		v, ok := vars[key]
		if !ok {
			missing[key] = true
			return m
		}
		return []byte(v)
	})
	if len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for k := range missing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("%s: placeholders sin valor: %s", name, strings.Join(keys, ", "))
	}
	return out, nil
}
