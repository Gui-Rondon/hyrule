// Command tags-api is another zero-business-logic example: a resource
// with a Unique field and a slug lookup, nothing else.
package main

import "github.com/Gui-Rondon/hyrule/app"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app.Main(spec())
}

func spec() app.Spec {
	return app.Spec{Name: "tags-api", DefaultDB: "tags", Version: version, Setup: setup}
}
