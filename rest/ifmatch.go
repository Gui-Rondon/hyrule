package rest

import (
	"slices"
	"strings"
)

// IfMatch is a parsed If-Match precondition (RFC 7232 §3.1): either the
// wildcard "*" (any current representation satisfies it) or a list of
// entity-tags, compared with the strong comparison function the RFC
// requires for If-Match — a weak entity-tag in the list can never match.
//
// WriteOptions.IfMatch carries this type (Fase 4b): http.go's update/
// delete handlers build it from the request's If-Match header(s) via
// ParseIfMatch, and Service.checkEtag/patchOnce's retry loop (section 9
// of the Postgres portability plan) consume it -- Any is what lets a
// PATCH under "If-Match: *" retry an optimistic-concurrency conflict
// instead of always answering the first one with 412.
type IfMatch struct {
	// Any is true for the wildcard "*".
	Any  bool
	tags []etag
}

type etag struct {
	value string
	weak  bool
}

// IsZero reports whether no If-Match condition was present at all —
// neither the wildcard nor any entity-tag — the same as the header being
// absent, or present but empty after RFC 7230 §7's tolerated empty list
// elements are discarded.
func (m IfMatch) IsZero() bool { return !m.Any && len(m.tags) == 0 }

// StrongMatches reports whether current — an unquoted, unweak etag value
// such as a stored "_etag" — satisfies this condition: true for the
// wildcard, or when current equals one of the listed entity-tags that is
// not itself weak (If-Match never matches by weak comparison).
func (m IfMatch) StrongMatches(current string) bool {
	if m.Any {
		return true
	}
	for _, t := range m.tags {
		if !t.weak && t.value == current {
			return true
		}
	}
	return false
}

// StrongMatchesAny is StrongMatches tried against several candidates
// in turn -- the stored etag plus, for a resource with CompatibleEtags,
// the older etags a write replaced -- true when any one of them
// satisfies m (or m is the wildcard, which never even looks at
// candidates).
func (m IfMatch) StrongMatchesAny(candidates ...string) bool {
	return m.Any || slices.ContainsFunc(candidates, m.StrongMatches)
}

// ParseIfMatch parses the value(s) of one or more If-Match header lines,
// e.g. http.Header.Values("If-Match") — RFC 7230 §3.2.2 lets a field
// repeat, each repetition appending to the same comma-separated list.
// ok is false when no usable element (wildcard or entity-tag) was found.
func ParseIfMatch(values []string) (IfMatch, bool) {
	var m IfMatch
	for _, v := range values {
		for _, elem := range splitEtagList(v) {
			elem = strings.TrimSpace(elem) // OWS around the comma, RFC 7230 §7
			if elem == "" {
				continue // tolerated empty list element
			}
			if elem == "*" {
				m.Any = true
				continue
			}
			// A malformed element (not "*", not a well-formed
			// [W/]"etagc*" entity-tag) is skipped, not an error: it
			// simply never matches, the same practical effect as a
			// client that sent a precondition this server does not
			// recognize.
			if t, ok := parseEntityTag(elem); ok {
				m.tags = append(m.tags, t)
			}
		}
	}
	return m, !m.IsZero()
}

// splitEtagList splits one header value on the comma list separator,
// without splitting inside a quoted entity-tag's content — etagc allows
// a literal comma, so a naive strings.Split would cut a tag like
// "a,b" in two.
func splitEtagList(v string) []string {
	var out []string
	start := 0
	inQuotes := false
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				out = append(out, v[start:i])
				start = i + 1
			}
		}
	}
	return append(out, v[start:])
}

// parseEntityTag parses one list element as an entity-tag: an optional
// "W/" weak marker followed by a DQUOTE-delimited run of etagc (RFC
// 7232 §2.3), the form a spec-compliant client echoes back from this
// server's own ETag responses (http.go always quotes them).
//
// A bare token with no surrounding DQUOTE is also accepted, as a
// strong (unweak) tag equal to the token itself -- not RFC 7232
// (DQUOTE is required there), but this project's own test suite and
// some of its custom endpoints have long sent If-Match back unquoted
// (e.g. testkit's created.String("_etag") passed straight into
// Client.Do), relying on the old string-based checkEtag's lenient
// trimEtag; rejecting that outright, now that If-Match is parsed
// properly, would be a real behavior regression, not a stricter
// correctness fix. Every etagc byte must still be valid either way --
// a literal DQUOTE in the middle of an otherwise bare token is still
// malformed and skipped.
//
// A bare token with the "W/" prefix is still weak, and so still never
// strong-matches (StrongMatches) -- unlike the old trimEtag, which
// stripped "W/" and compared the rest as plain strings with no concept
// of weak at all. No known caller sends a bare weak tag, so this isn't
// the same kind of compatibility concern the plain bare-token case
// above is; TestParseIfMatchBareWeakTagNeverStrongMatches pins it down
// as the deliberately correct (RFC 7232 §3.1) behavior, not an
// untested side effect.
func parseEntityTag(s string) (etag, bool) {
	weak := false
	if after, ok := strings.CutPrefix(s, "W/"); ok {
		weak = true
		s = after
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	for i := 0; i < len(s); i++ {
		if !isEtagc(s[i]) {
			return etag{}, false
		}
	}
	return etag{value: s, weak: weak}, true
}

// isEtagc reports whether b is a valid etagc (RFC 7232 §2.3): VCHAR
// except double quote, or obs-text (0x80-0xFF, legacy non-UTF-8 etags).
func isEtagc(b byte) bool {
	return b == 0x21 || (b >= 0x23 && b <= 0x7E) || b >= 0x80
}
