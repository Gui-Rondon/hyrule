package slug

import "testing"

func TestMake(t *testing.T) {
	for in, want := range map[string]string{
		"Futebol Gaúcho":        "futebol-gaucho",
		"São Paulo x Grêmio!":   "sao-paulo-x-gremio",
		"  Eleições 2026 - RS ": "eleicoes-2026-rs",
		"Ação'Já":               "acao-ja",
		// python-slugify 7.0.0 (text-unidecode), contract/contents-index
		"Straße":         "strasse",
		"Æon Flux":       "aeon-flux",
		"Nº 1 — “aspas”": "no-1-aspas",
		"øØ œŒ đĐ łŁ þÞ": "oo-oeoe-dd-ll-thth",
		"ª º ½ ™ € © ®":  "a-o-1-2-tm-eur-c-r",
		"° µ × ĳ ŀ ſ":    "deg-u-x-ij-l-s",
		"  ":             "",
	} {
		if got := Make(in); got != want {
			t.Errorf("Make(%q) = %q, want %q", in, got, want)
		}
	}
}
