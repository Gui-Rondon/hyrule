// Command hyrule-serve registers the resources declared in a YAML file
// (declarative) and serves them — no domain-specific Go code at
// all for the common case the loader covers.
//
//	RESOURCES_FILE=resources.yaml PORT=8080 go run ./cmd/hyrule-serve
//
// A resource that needs a custom hook, endpoint or validation still needs
// its own Go service (app.Main with a Setup func) — hyrule-serve only
// covers what declarative can build from data.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/declarative"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app.Main(spec())
}

func spec() app.Spec {
	return app.Spec{Name: "hyrule-serve", DefaultDB: "hyrule", Version: version, Setup: setup}
}

func setup(_ context.Context, env *app.Env) error {
	path := env.Config.String("resources_file", os.Getenv("RESOURCES_FILE"))
	if path == "" {
		return errors.New("hyrule-serve: RESOURCES_FILE (or resources_file in the config) is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("hyrule-serve: %w", err)
	}
	defer f.Close()
	file, err := declarative.Parse(f)
	if err != nil {
		return fmt.Errorf("hyrule-serve: %s: %w", path, err)
	}
	for _, r := range file.Resources {
		res, err := r.Build(env.Store(r.Collection))
		if err != nil {
			return fmt.Errorf("hyrule-serve: %s: %w", path, err)
		}
		env.API.Register(res)
	}
	return nil
}
