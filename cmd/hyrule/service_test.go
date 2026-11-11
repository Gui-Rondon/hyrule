package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunService(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	if err := runService([]string{"create", "articles"}); err == nil {
		t.Fatal(`"services" doesn't exist yet, this should fail`)
	}
	if err := os.Mkdir("services", 0o755); err != nil {
		t.Fatal(err)
	}

	if err := runService([]string{"create", "articles"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main.go", "resource.go", "resource_test.go"} {
		path := filepath.Join(dir, "services", "articles", want)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}

	res, err := os.ReadFile(filepath.Join(dir, "services", "articles", "resource.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res), `Name:      "articles"`) {
		t.Errorf("resource.go doesn't name the resource: %s", res)
	}

	if err := runService([]string{"create", "articles"}); err == nil {
		t.Error("recreating an existing service should fail")
	}
	if err := runService([]string{"create", "Not-Valid"}); err == nil {
		t.Error("an invalid service name should be rejected")
	}
	if err := runService([]string{"new", "articles"}); err == nil {
		t.Error("only create is a valid subcommand")
	}
}
