package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

//go:embed all:templates/new
var newTemplates embed.FS

var validProjectName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func runNew(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: hyrule new <project>")
	}
	name := args[0]
	if !validProjectName.MatchString(name) {
		return fmt.Errorf("project name must be lowercase letters, digits and hyphens, starting with a letter")
	}
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	}

	data := struct{ Module string }{Module: name}
	if err := renderTemplateDir(newTemplates, "templates/new", name, data); err != nil {
		return err
	}
	// services/ starts empty — "hyrule service create" populates it. Not
	// part of the embedded template tree: go:embed can't carry an empty
	// directory.
	if err := os.MkdirAll(filepath.Join(name, "services"), 0o755); err != nil {
		return err
	}

	fmt.Printf("created %s\n\nnext:\n  cd %s\n  go get github.com/Gui-Rondon/hyrule@main\n  hyrule service create <name>\n", name, name)
	return nil
}
