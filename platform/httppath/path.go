// Package httppath contains the path operations shared by Hyrule's HTTP
// router and REST helpers.
package httppath

import "strings"

// NormalizePrefix turns a URL prefix into its canonical form: either empty
// (no prefix) or one leading slash with no trailing slash. A prefix of "/"
// is equivalent to no prefix.
func NormalizePrefix(prefix string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return ""
	}
	return "/" + prefix
}

// StripPrefix removes a canonical URL prefix when path is exactly that
// prefix or a path below it. It returns the inner router path with a leading
// slash, including "/" when path equals prefix.
func StripPrefix(path, prefix string) (tail string, ok bool) {
	if prefix == "" {
		return path, true
	}
	tail, ok = strings.CutPrefix(path, prefix)
	if !ok || (tail != "" && !strings.HasPrefix(tail, "/")) {
		return "", false
	}
	if tail == "" {
		tail = "/"
	}
	return tail, true
}

// TrimOuterSlashes returns path without leading or trailing slashes. REST
// link generation uses this for the request path it echoes in response links.
func TrimOuterSlashes(path string) string {
	return strings.Trim(path, "/")
}
