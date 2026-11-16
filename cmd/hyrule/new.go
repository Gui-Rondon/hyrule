package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

//go:embed all:templates/new all:templates/single
var newTemplates embed.FS
var validProjectName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func runNew(args []string) error {
	// Accept flags before or after the name, matching the documented CLI.
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	layout := flags.String("layout", "single", "single or platform")
	version := flags.String("framework-version", "", "pinned framework version for development CLI builds")
	options := []string{}
	names := []string{}
	for i := 0; i < len(args); i++ {
		if len(args[i]) > 0 && args[i][0] == '-' {
			options = append(options, args[i])
			if (args[i] == "--layout" || args[i] == "--framework-version") && i+1 < len(args) {
				i++
				options = append(options, args[i])
			}
		} else {
			names = append(names, args[i])
		}
	}
	if err := flags.Parse(options); err != nil {
		return err
	}
	if len(names) != 1 {
		return fmt.Errorf("usage: hyrule new <project> [--layout=single|platform] [--framework-version=vX.Y.Z]")
	}
	name := names[0]
	if !validProjectName.MatchString(name) {
		return fmt.Errorf("project name must be lowercase letters, digits and hyphens, starting with a letter")
	}
	if *layout != "single" && *layout != "platform" {
		return fmt.Errorf("layout must be single or platform")
	}
	pinned, err := frameworkVersion(*version)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(name); !os.IsNotExist(err) {
		return fmt.Errorf("%s already exists or cannot be accessed", name)
	}
	// Render in a temporary sibling; errors do not leave a half-created project.
	temp, err := os.MkdirTemp(".", ".hyrule-new-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	base := "templates/single"
	backend := "sqlite"
	if *layout == "platform" {
		base = "templates/new"
		backend = "mongo"
	}
	data := struct{ Module, Version string }{name, pinned}
	if err := renderTemplateDir(newTemplates, base, temp, data); err != nil {
		return err
	}
	if *layout == "platform" {
		if err := os.MkdirAll(filepath.Join(temp, "services"), 0755); err != nil {
			return err
		}
	}
	raw, _ := json.MarshalIndent(projectConfig{*layout, backend}, "", "  ")
	if err := os.WriteFile(filepath.Join(temp, "hyrule.json"), append(raw, '\n'), 0644); err != nil {
		return err
	}
	if err := os.Rename(temp, name); err != nil {
		return err
	}
	fmt.Printf("created %s (%s/%s)\n\nnext:\n  cd %s\n  hyrule service create products\n", name, *layout, backend, name)
	return nil
}
