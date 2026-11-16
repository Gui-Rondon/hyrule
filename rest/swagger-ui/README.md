# Swagger UI assets

Unmodified distribution assets from swagger-api/swagger-ui **v5.35.0**:
https://github.com/swagger-api/swagger-ui/tree/v5.35.0/dist

`swagger-ui-bundle.js`, `swagger-ui.css`, and the upstream Apache-2.0
`LICENSE` are embedded in the Go binary. Updating them is an explicit,
versioned dependency update; consumers need neither Node nor a CDN.

The initializer is in `rest/swagger.go`. It disables the remote validator
and token persistence, and resolves URLs relative to the external page URL.
