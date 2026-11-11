// Command standalone-tags-api is the Fase 2 proof for Hyrule's Framework
// path: a resource with a YAML-declared schema plus one small Go hook
// (audit), built by a module that depends on Hyrule the way an external
// team would — go get, not an import of anything under internal/ (there's
// nothing left to import there for this: see
// github.com/Gui-Rondon/hyrule/website/docs/reference/architecture-decisions.md,
// ADR-0001).
//
//	MONGO_CONNECTION_STRING='mongodb://localhost:27018/standalone_demo?directConnection=true' \
//	  CONFIG_FILE=config.json PORT=8080 go run .
package main

import (
	"context"
	"embed"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/behaviors/audit"
	"github.com/Gui-Rondon/hyrule/declarative"
)

//go:embed resources.yaml
var resourcesFS embed.FS

var version = "dev"

func main() {
	app.Main(spec())
}

func spec() app.Spec {
	return app.Spec{Name: "standalone-tags-api", DefaultDB: "standalone_demo", Version: version, Setup: setup}
}

func setup(_ context.Context, env *app.Env) error {
	f, err := resourcesFS.Open("resources.yaml")
	if err != nil {
		return err
	}
	defer f.Close()
	file, err := declarative.Parse(f)
	if err != nil {
		return err
	}
	for _, r := range file.Resources {
		res, err := r.Build(env.Store(r.Collection))
		if err != nil {
			return err
		}
		res.Hooks = audit.Hooks()
		env.API.Register(res)
	}
	return nil
}
