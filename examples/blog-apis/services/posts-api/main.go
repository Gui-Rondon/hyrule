// Command posts-api is the richest example of the blog-apis suite: a
// resource with relations to authors-api/categories-api/tags-api, custom
// publish/schedule endpoints, outbox events, a Redis-backed scheduling
// index and Elasticsearch indexing.
package main

import "github.com/Gui-Rondon/hyrule/internal/app"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app.Main(spec())
}

func spec() app.Spec {
	return app.Spec{Name: "posts-api", DefaultDB: "posts", Version: version, Setup: setup}
}
