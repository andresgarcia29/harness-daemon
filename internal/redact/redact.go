// Package redact tapa secretos ANTES de que toquen el disco. El colector guarda
// texto libre de los transcripts (razonamiento, herramientas): un token que se
// cuele ahí queda persistido. Mismos patrones que emit.sh y el panel de Python
// — la ley de secretos es una sola, en todos los lenguajes del harness.
package redact

import "regexp"

type rule struct {
	re   *regexp.Regexp
	repl string
}

var rules = []rule{
	{regexp.MustCompile(`(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`), "[REDACTADO:gh]"},
	{regexp.MustCompile(`(hvs|hvb)\.[A-Za-z0-9_-]{20,}`), "[REDACTADO:vault]"},
	{regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`), "[REDACTADO:key]"},
	{regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`), "[REDACTADO:slack]"},
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`), "[REDACTADO:jwt]"},
	{regexp.MustCompile(`(AKIA|ASIA)[A-Z0-9]{12,}`), "[REDACTADO:aws]"},
	{regexp.MustCompile(`lin_api_[A-Za-z0-9]{20,}`), "[REDACTADO:linear]"},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), "[REDACTADO:privkey]"},
	{regexp.MustCompile(`(?i)((password|passwd|secret|token|api[_-]?key|authorization)["']?\s*[:=]\s*["']?)([^\s"',}]{6,})`), "$1[REDACTADO]"},
}

// String redacta un texto; String("") devuelve "".
func String(s string) string {
	if s == "" {
		return s
	}
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// Clip redacta y recorta a n runas (con marca si se cortó).
func Clip(s string, n int) string {
	s = String(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + " […]"
}
