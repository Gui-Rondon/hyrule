// Command affected prints the services whose binary changed between two
// commits, one per line, for the CI "detect services" job.
//
//	go run ./ci/affected <base>                       # files changed between base and HEAD
//	go run ./ci/affected -changed a,b,c               # an explicit list (tests, debugging)
//	go run ./ci/affected -services path/to/services <base>  # non-default services dir
//
// A Go binary holds exactly the packages it imports, so a service is selected
// when a changed file is one of the files of its build (the non test .go
// files and the embedded files of its packages, from go list -deps), when
// a non test .go file of one of its package directories changed (a deleted
// file is no longer in the build list), when one of its files under
// <dir-of-services>/deploy/<service>/ changed, or when the version of a
// module it uses changed in go.mod/go.sum. Changes to what every image
// shares (Dockerfile, the Go version, the CI files) select every service.
//
// -services defaults to examples/blog-apis/services, the one
// reference adopter shipped in this repo; a consumer of the framework with
// its own services directory passes -services to point at it.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// everyService are paths whose change rebuilds all images.
var everyService = []string{"Dockerfile", "ci/", ".dockerignore"}

// defaultServicesDir is the services directory of the reference adopter
// shipped in this repo.
const defaultServicesDir = "examples/blog-apis/services"

// Build is what a service binary is made of.
type Build struct {
	// Files are repository paths (relative to the module root).
	Files map[string]bool
	// Dirs are the directories of its packages (repository paths).
	Dirs map[string]bool
	// Modules are the external modules the binary uses.
	Modules map[string]bool
}

// Change is what changed between the two commits.
type Change struct {
	Files []string
	// Modules whose version changed (from the go.sum diff).
	Modules []string
	// Toolchain is true when the go or toolchain directive changed.
	Toolchain bool
}

// Select returns the services affected by c, sorted. deployDir is the
// deploy/ directory next to servicesDir (deployDir+"/"+service+"/" holds
// that service's manifests).
func Select(services map[string]Build, c Change, deployDir string) []string {
	all := make([]string, 0, len(services))
	for s := range services {
		all = append(all, s)
	}
	sort.Strings(all)
	if c.Toolchain {
		return all
	}
	for _, f := range c.Files {
		for _, p := range everyService {
			if f == p || strings.HasSuffix(p, "/") && strings.HasPrefix(f, p) {
				return all
			}
		}
	}
	var out []string
	for _, s := range all {
		b := services[s]
		if slices.ContainsFunc(c.Files, func(f string) bool {
			return b.Files[f] || strings.HasPrefix(f, deployDir+"/"+s+"/") ||
				strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") && b.Dirs[path.Dir(f)]
		}) || slices.ContainsFunc(c.Modules, func(m string) bool { return b.Modules[m] }) {
			out = append(out, s)
		}
	}
	return out
}

func main() {
	changed := flag.String("changed", "", "comma separated changed files, instead of git diff <base> HEAD")
	servicesDir := flag.String("services", defaultServicesDir, "directory with one Go binary per subdirectory")
	flag.Parse()
	if err := run(*changed, flag.Arg(0), *servicesDir, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "affected:", err)
		os.Exit(1)
	}
}

func run(changed, base, servicesDir string, w io.Writer) error {
	var c Change
	var err error
	switch {
	case changed != "":
		c.Files = strings.Split(changed, ",")
	case base != "":
		if c, err = gitChange(base); err != nil {
			return err
		}
	default:
		return errors.New("usage: affected <base> | affected -changed a,b")
	}
	services, err := builds(servicesDir)
	if err != nil {
		return err
	}
	deployDir := path.Join(path.Dir(filepath.ToSlash(servicesDir)), "deploy")
	for _, s := range Select(services, c, deployDir) {
		fmt.Fprintln(w, s)
	}
	return nil
}

// builds reads the build of every <servicesDir>/<name> with go list.
func builds(servicesDir string) (map[string]Build, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	dirs, err := os.ReadDir(filepath.Join(root, servicesDir))
	if err != nil {
		return nil, err
	}
	out := map[string]Build{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		b, err := build(root, "./"+filepath.ToSlash(filepath.Join(servicesDir, d.Name())))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.Name(), err)
		}
		out[d.Name()] = b
	}
	return out, nil
}

type pkg struct {
	Dir        string
	GoFiles    []string
	CgoFiles   []string
	EmbedFiles []string
	Standard   bool
	Module     *struct {
		Path    string
		Main    bool
		Replace *struct{ Path string }
	}
}

func build(root, target string) (Build, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", target)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return Build{}, fmt.Errorf("go list: %w", err)
	}
	b := Build{Files: map[string]bool{}, Dirs: map[string]bool{}, Modules: map[string]bool{}}
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			return Build{}, err
		}
		if p.Standard || p.Module == nil {
			continue
		}
		if !p.Module.Main {
			b.Modules[p.Module.Path] = true
			continue
		}
		rel, err := filepath.Rel(root, p.Dir)
		if err != nil {
			return Build{}, err
		}
		b.Dirs[filepath.ToSlash(rel)] = true
		for _, files := range [][]string{p.GoFiles, p.CgoFiles, p.EmbedFiles} {
			for _, f := range files {
				b.Files[filepath.ToSlash(filepath.Join(rel, f))] = true
			}
		}
	}
	return b, nil
}

func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("not inside the module")
	}
	return filepath.Dir(gomod), nil
}

// gitChange is git diff <base> HEAD: the files, and from go.mod/go.sum the
// modules whose version changed and whether the go/toolchain line changed.
func gitChange(base string) (Change, error) {
	var c Change
	// -z: paths are NUL separated and never quoted
	out, err := exec.Command("git", "diff", "--name-only", "-z", base, "HEAD").Output()
	if err != nil {
		return c, fmt.Errorf("git diff: %w", err)
	}
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			c.Files = append(c.Files, f)
		}
	}
	if !slices.Contains(c.Files, "go.mod") && !slices.Contains(c.Files, "go.sum") {
		return c, nil
	}
	diff, err := exec.Command("git", "diff", "-U0", base, "HEAD", "--", "go.mod", "go.sum").Output()
	if err != nil {
		return c, fmt.Errorf("git diff go.mod: %w", err)
	}
	c.Modules, c.Toolchain = parseModDiff(diff)
	// go.mod and go.sum themselves are not part of any build file list
	c.Files = slices.DeleteFunc(c.Files, func(f string) bool { return f == "go.mod" || f == "go.sum" })
	return c, nil
}

// parseModDiff reads the changed lines of go.mod and go.sum: "<module>
// <version>[/go.mod] <hash>" in go.sum, "<module> <version>" in require
// blocks, and the go/toolchain directives.
func parseModDiff(diff []byte) (modules []string, toolchain bool) {
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(diff))
	for sc.Scan() {
		line := sc.Text()
		if len(line) < 2 || line[0] != '+' && line[0] != '-' || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line[1:]))
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "go", "toolchain":
			toolchain = true
			continue
		case "require", "replace", "exclude", "module", ")", "(":
			if len(fields) < 3 {
				continue
			}
			fields = fields[1:]
		}
		if len(fields) >= 2 && strings.Contains(fields[0], ".") && !seen[fields[0]] {
			seen[fields[0]] = true
			modules = append(modules, fields[0])
		}
	}
	sort.Strings(modules)
	return modules, toolchain
}
