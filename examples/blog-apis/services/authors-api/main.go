// Command authors-api is the simplest example of the blog-apis suite: a
// resource described entirely by its schema, with zero business-specific
// Go code — a candidate to become a plain YAML definition once the
// framework's declarative resource loader exists.
package main

import "github.com/Gui-Rondon/hyrule/internal/app"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app.Main(spec())
}

func spec() app.Spec {
	return app.Spec{Name: "authors-api", DefaultDB: "authors", Version: version, Setup: setup}
}
