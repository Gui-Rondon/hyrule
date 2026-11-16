package main

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		fail        bool
	}{
		{"quoted commas", "label,success\n\"publish, then receive\",true\n", false},
		{"assertion failure", "label,success\nfailed,false\n", true},
		{"missing success column", "label,result\na,true\n", true},
		{"empty results", "label,success\n", true},
		{"malformed CSV", "label,success\na\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := check(strings.NewReader(tc.input)); (err != nil) != tc.fail {
				t.Fatalf("check: %v", err)
			}
		})
	}
}
