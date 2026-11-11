package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunNew(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	if err := runNew([]string{"my-platform"}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"go.mod", "Makefile", "docker-compose.yml", "README.md", ".gitignore"} {
		path := filepath.Join(dir, "my-platform", want)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "my-platform", "services")); err != nil || !info.IsDir() {
		t.Errorf("services/ not created as a directory: %v", err)
	}

	mod, err := os.ReadFile(filepath.Join(dir, "my-platform", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "module my-platform") {
		t.Errorf("go.mod = %q, missing module name", mod)
	}

	if err := runNew([]string{"my-platform"}); err == nil {
		t.Error("recreating an existing project should fail")
	}
	if err := runNew([]string{"Not-Valid"}); err == nil {
		t.Error("an invalid project name should be rejected")
	}
	if err := runNew(nil); err == nil {
		t.Error("missing project name should be rejected")
	}
}
