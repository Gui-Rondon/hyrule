package rest_test

import (
	"testing"

	"github.com/Gui-Rondon/hyrule/rest"
)

func TestParseIfMatchWildcard(t *testing.T) {
	m, ok := rest.ParseIfMatch([]string{"*"})
	if !ok || !m.Any {
		t.Fatalf("got %+v, ok=%v, want Any=true", m, ok)
	}
	if !m.StrongMatches("anything") {
		t.Error("wildcard must match any current etag")
	}
	if m.IsZero() {
		t.Error("wildcard is not a zero IfMatch")
	}
}

func TestParseIfMatchSingleStrongTag(t *testing.T) {
	m, ok := rest.ParseIfMatch([]string{`"abc123"`})
	if !ok {
		t.Fatal("want ok=true")
	}
	if !m.StrongMatches("abc123") {
		t.Error("want the quoted tag to strong-match its own value")
	}
	if m.StrongMatches("other") {
		t.Error("must not match a different etag")
	}
}

func TestParseIfMatchWeakTagNeverStrongMatches(t *testing.T) {
	// If-Match requires the strong comparison function (RFC 7232 §3.1):
	// a weak entity-tag can never satisfy it, even against its own value.
	m, ok := rest.ParseIfMatch([]string{`W/"abc123"`})
	if !ok {
		t.Fatal("want ok=true: a weak tag is still a present condition")
	}
	if m.StrongMatches("abc123") {
		t.Error("a weak entity-tag must never strong-match")
	}
}

func TestParseIfMatchCommaSeparatedList(t *testing.T) {
	m, ok := rest.ParseIfMatch([]string{`"a", "b", "c"`})
	if !ok {
		t.Fatal("want ok=true")
	}
	for _, want := range []string{"a", "b", "c"} {
		if !m.StrongMatches(want) {
			t.Errorf("want %q to match", want)
		}
	}
	if m.StrongMatches("d") {
		t.Error("must not match a tag outside the list")
	}
}

func TestParseIfMatchCommaInsideQuotedTag(t *testing.T) {
	// etagc allows a literal comma inside the quoted content; splitting
	// naively on every comma would cut this tag in two.
	m, ok := rest.ParseIfMatch([]string{`"a,b"`})
	if !ok {
		t.Fatal("want ok=true")
	}
	if !m.StrongMatches("a,b") {
		t.Error("want the comma preserved inside the single tag")
	}
}

func TestParseIfMatchMultipleHeaderLines(t *testing.T) {
	// RFC 7230 §3.2.2: a repeated header appends to the same list.
	m, ok := rest.ParseIfMatch([]string{`"a"`, `"b"`})
	if !ok {
		t.Fatal("want ok=true")
	}
	if !m.StrongMatches("a") || !m.StrongMatches("b") {
		t.Error("want both tags, from both header lines, to match")
	}
}

func TestParseIfMatchEmptyListElementsTolerated(t *testing.T) {
	m, ok := rest.ParseIfMatch([]string{`"a", , "b"`})
	if !ok {
		t.Fatal("want ok=true")
	}
	if !m.StrongMatches("a") || !m.StrongMatches("b") {
		t.Error("want the empty element between the two tags ignored")
	}
}

func TestParseIfMatchMalformedElementSkipped(t *testing.T) {
	m, ok := rest.ParseIfMatch([]string{`not-a-valid-tag, "b"`})
	if !ok {
		t.Fatal("want ok=true: the valid tag still counts")
	}
	if !m.StrongMatches("b") {
		t.Error("want the well-formed tag to still be parsed")
	}
	if m.StrongMatches("not-a-valid-tag") {
		t.Error("an unquoted element must never match")
	}
}

func TestParseIfMatchAbsentOrEmpty(t *testing.T) {
	for _, values := range [][]string{nil, {}, {""}, {" , "}} {
		m, ok := rest.ParseIfMatch(values)
		if ok {
			t.Errorf("ParseIfMatch(%q): want ok=false", values)
		}
		if !m.IsZero() {
			t.Errorf("ParseIfMatch(%q): want a zero IfMatch", values)
		}
		if m.StrongMatches("anything") {
			t.Errorf("ParseIfMatch(%q): a zero IfMatch must not match", values)
		}
	}
}
