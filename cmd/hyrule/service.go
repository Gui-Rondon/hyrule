package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

//go:embed templates/service
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
	if _, err := os.Stat("services"); err != nil {
		return fmt.Errorf(`"services" directory not found — run this from the root of a project created with "hyrule new"`)
	}
	dest := filepath.Join("services", name)
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%s already exists", dest)
	}

	data := struct{ Name string }{Name: name}
	if err := renderTemplateDir(serviceTemplates, "templates/service", dest, data); err != nil {
		return err
	}

	fmt.Printf("created %s\n\nnext:\n  make build SERVICE=%s\n  make run SERVICE=%s\n", dest, name, name)
	return nil
}
