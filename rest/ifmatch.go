package rest

import "strings"

// IfMatch is a parsed If-Match precondition (RFC 7232 §3.1): either the
// wildcard "*" (any current representation satisfies it) or a list of
// entity-tags, compared with the strong comparison function the RFC
// requires for If-Match — a weak entity-tag in the list can never match.
//
// Fase 0 of the Postgres portability work (ADR-0002): this type and
// ParseIfMatch exist so a future backend-agnostic Service can express
// "match any" (If-Match: *, with retry — see the Postgres plan's CAS
// retry loop) without re-parsing the header at every call site. Nothing
// in this package constructs or consumes an IfMatch yet — rest/http.go
// still carries the header as the bare string it always has
// (WriteOptions.IfMatch); wiring this in is later work, together with
// the rest of the HTTP/Service integration.
type IfMatch struct {
	// Any is true for the wildcard "*".
	Any bool
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
// "W/" weak marker followed by a DQUOTE-delimited run of etagc.
func parseEntityTag(s string) (etag, bool) {
	weak := false
	if after, ok := strings.CutPrefix(s, "W/"); ok {
		weak = true
		s = after
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return etag{}, false
	}
	inner := s[1 : len(s)-1]
	for i := 0; i < len(inner); i++ {
		if !isEtagc(inner[i]) {
			return etag{}, false
		}
	}
	return etag{value: inner, weak: weak}, true
}

// isEtagc reports whether b is a valid etagc (RFC 7232 §2.3): VCHAR
// except double quote, or obs-text (0x80-0xFF, legacy non-UTF-8 etags).
func isEtagc(b byte) bool {
	return b == 0x21 || (b >= 0x23 && b <= 0x7E) || b >= 0x80
}
