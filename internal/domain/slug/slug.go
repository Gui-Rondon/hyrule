// Package slug is python-slugify's default slugify, used by the APIs and
// workers that turn tag names into {slug, name}.
package slug

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// unidecode is what text-unidecode (python-slugify's transliteration) does
// for the letters and symbols that NFKD does not decompose into ASCII.
var unidecode = strings.NewReplacer(
	"ß", "ss", "ẞ", "SS", "æ", "ae", "Æ", "AE", "ø", "o", "Ø", "O", "œ", "oe", "Œ", "OE",
	"đ", "d", "Đ", "D", "ð", "d", "Ð", "D", "ł", "l", "Ł", "L", "þ", "th", "Þ", "Th",
	"ı", "i", "ŋ", "ng", "Ŋ", "Ng", "ĸ", "k", "ſ", "s", "ħ", "h", "Ħ", "H", "ŧ", "t", "Ŧ", "T",
	"ŀ", "l", "Ŀ", "L", "€", "EUR", "©", "(c)", "®", "(r)", "°", "deg", "µ", "u", "×", "x",
)

// Make transliterates to ASCII, lowers the case and turns runs of other
// characters (quotes included) into one hyphen.
func Make(s string) string {
	t := transform.Chain(norm.NFKD, runes.Remove(runes.In(unicode.Mn)))
	ascii, _, _ := transform.String(t, unidecode.Replace(s))
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(ascii), "-"), "-")
}
