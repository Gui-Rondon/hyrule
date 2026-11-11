package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffoldBuilds is the end-to-end proof that "hyrule new" + "hyrule
// service create" produce a usable project, not just files on disk: the
// same flow a person actually runs (wire the dependency, generate a
// service, build it with the generated Makefile) against a real Go
// toolchain. No Docker/Mongo required for this part — see the
// MONGO_TEST_URI branch below for the one step that does need it.
func TestScaffoldBuilds(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// The module root of this checkout, so the generated project can
	// "replace" Hyrule with it instead of a real "go get" — Hyrule isn't
	// tagged yet (ADR-0005), so there's nothing to go get.
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(strings.TrimSpace(string(gomod)))

	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	if err := runNew([]string{"my-platform"}); err != nil {
		t.Fatalf("hyrule new: %v", err)
	}

	project := filepath.Join(dir, "my-platform")
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}

	run := func(name string, args ...string) string {
		cmd := exec.Command(name, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	run("go", "mod", "edit",
		"-require=github.com/Gui-Rondon/hyrule@v0.0.0",
		"-replace=github.com/Gui-Rondon/hyrule="+repoRoot)

	if err := runService([]string{"create", "articles"}); err != nil {
		t.Fatalf("hyrule service create: %v", err)
	}

	run("go", "mod", "tidy")
	run("go", "vet", "./...")

	// The real flow a person follows, via the generated Makefile itself
	// (not a hand-rolled "go build" that would miss a regression in the
	// Makefile template).
	run("make", "build", "SERVICE=articles")

	bin := filepath.Join(project, ".local", "articles")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("make build should have produced %s: %v", bin, err)
	}

	// "make build" with no SERVICE must fail clearly, not run a broken
	// go build silently.
	cmd := exec.Command("make", "build")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Error("make build with no SERVICE should fail")
	}
	if !strings.Contains(string(out), "SERVICE is required") {
		t.Errorf("make build with no SERVICE = %q, want a message naming SERVICE", out)
	}

	// The generated resource_test.go needs a real Mongo (testkit.Service),
	// same as every other test in this framework — skip it here the same
	// way the rest of the repo skips Mongo-backed tests when
	// MONGO_TEST_URI isn't set, rather than requiring Docker for this test.
	mongo := os.Getenv("MONGO_TEST_URI")
	if mongo == "" {
		t.Log("MONGO_TEST_URI not set: skipping the generated project's own `go test` (needs a real Mongo) — covered manually, see the PR description")
		return
	}
	cmd = exec.Command("go", "test", "./...")
	cmd.Env = append(os.Environ(), "MONGO_TEST_URI="+mongo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("go test ./... in the generated project: %v\n%s", err, out)
	}
}
