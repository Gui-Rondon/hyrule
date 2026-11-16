package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed templates/service templates/single-service
var serviceTemplates embed.FS

var validServiceName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func runService(args []string) error {
	if len(args) != 2 || args[0] != "create" {
		return fmt.Errorf("usage: hyrule service create <name>")
	}
	name := args[1]
	if !validServiceName.MatchString(name) {
		return fmt.Errorf("service name must be lowercase letters, digits and hyphens, starting with a letter")
	}
	cfg, err := readProject()
	if err != nil {
		return err
	}
	if cfg.Layout == "single" {
		return createSingleResource(name)
	}

	dest := filepath.Join("services", name)
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%s already exists", dest)
	}

	data := struct{ Name string }{Name: name}
	if err := renderTemplateDir(serviceTemplates, "templates/service", dest, data); err != nil {
		return err
	}

	fmt.Printf("created %s\n\nnext:\n  go mod tidy\n  make build SERVICE=%s\n  make run SERVICE=%s\n", dest, name, name)
	return nil
}

func createSingleResource(name string) error {
	symbol := strings.ReplaceAll(name, "-", "_")
	dest := "resource_" + symbol + "_generated.go"
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return fmt.Errorf("%s already exists or cannot be accessed", dest)
	}
	temp, err := os.MkdirTemp(".", ".hyrule-resource-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	data := struct{ Name, Table, Symbol string }{name, symbol, symbol}
	if err := renderTemplateDir(serviceTemplates, "templates/single-service", temp, data); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(temp, "resource.go"))
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	closeErr := f.Close()
	if writeErr != nil {
		os.Remove(dest)
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Stdout = os.Stdout
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("created %s, but dependency resolution failed: run go mod tidy: %w", dest, err)
	}
	fmt.Printf("created %s\n\nnext:\n  go run .\n", dest)
	return nil
}
