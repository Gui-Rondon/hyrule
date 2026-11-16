# Compose Hyrule with Gin, Chi or Echo

This standalone module tests the current checkout via a local `replace`.
With Go 1.26 or 1.27, run `go run .` (Chi), `ROUTER=gin go run .` or
`ROUTER=echo go run .`. Open `/hyrule/api/docs/` and POST JSON to
`/hyrule/products`. SQLite persists in hyrule.db; authentication is disabled.

`Mount` passes the full request URL to an ordinary http.Handler.
`app.Spec.URLPrefix` strips `/hyrule` exactly once. Do not also use StripPrefix:
that would remove the prefix twice. Gin/Chi/Echo can own other application routes
and middleware; Hyrule retains resource routing internally. Host middleware applies
to the handler as usual, while Hyrule authentication and validation still apply.

`go test -race ./...` checks CRUD, ETags, 404/405, outer middleware, item lookups
and Swagger under the prefix. To use a published release after this work ships,
remove the replace directive and require the corresponding release version.
The router dependencies live only in this example module, not in Hyrule's core.
