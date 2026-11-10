// Command categories-api demonstrates a hierarchical resource: a
// materialized path kept up to date on every write, and a custom
// GET /categories/tree endpoint built straight from the Store interface
// (no raw *mongo.Collection in sight).
package main

import "github.com/Gui-Rondon/hyrule/internal/app"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app.Main(spec())
}

func spec() app.Spec {
	return app.Spec{Name: "categories-api", DefaultDB: "categories", Version: version, Setup: setup}
}
