# Automatic Swagger documentation

Swagger is **enabled by default** for every resource registered through
`rest.API.Register`, including Go and YAML resources. No annotations, code
generation, database connection, or consumer-side Node installation is needed.

| URL | Content |
|---|---|
| `/api/docs` | Swagger UI, served with local assets |
| `/swagger` | Redirect to `/api/docs` |
| `/api/openapi.json` | OpenAPI 3.1.0 specification |

The page and specification are public. The **Authorize** button accepts a
Bearer token for protected operations; public methods remain public. Opening
the page does not change API authentication. The UI does not persist tokens or
send the specification to an external validator.

## Hide a resource

Go uses the exported field `DisableSwagger`:

```go
env.API.Register(&rest.Resource{
    Name:           "internal-events",
    Store:          env.Store("internal-events"),
    DisableSwagger: true,
})
```

YAML accepts `disableSwagger` or the equivalent `disable_swagger`:

```yaml
resources:
  - name: tags
    collection: tags
    # Omitted or false: included in Swagger by default.
    disableSwagger: false
    schema:
      name: {type: string, required: true}
  - name: internal-events
    collection: internal-events
    disableSwagger: true
```

Conflicting values for the two YAML spellings fail resource construction.
Setting the flag to true hides that resource's operations and schemas from the
specification. CRUD, hooks, authentication and the API home listing continue
working. This flag controls documentation visibility, not access permissions.
If every resource is hidden, the page remains available with an empty spec.
There is no global disable switch in this version.

## Generated contract

The document follows the registered resource URL and effective defaults. It
includes enabled CRUD methods, schemas, nested objects and lists, separate
full-write/PATCH/response schemas, individual and batch POST, query parameters,
pagination envelopes, read-only additional lookups, ETag/If-Match, errors and
per-method authentication. It supports both Mongo and Postgres resources. IDs
are documented as strings without assuming every adapter uses ObjectIDs.
Dates use Eve's `DATE_FORMAT`, rather than advertising RFC 3339 responses.

Unicity, Go regular expressions and validation callbacks are server rules:
`x-hyrule-unique`, `x-hyrule-regex`, and `x-hyrule-custom-validation` preserve
those details without promising they can run in a browser. Hooks and custom
validators can impose rules beyond the generated schema.

Custom endpoints registered with `API.Handle`, health checks and metrics are
not included. Resource registration and definitions are startup configuration;
do not mutate them concurrently with HTTP requests.


When `Resource.Validate` is configured, full-write and PATCH input components
also carry `x-hyrule-custom-validation: true` at the object level. The same marker
on a property identifies `Field.Check`. It describes additional server rules;
it neither executes callbacks nor lets a JSON Schema validator reproduce them.
The resource-level marker is not added to response components.

### PATCH and nested required fields

PATCH permits omitted required fields at the document root. When an object is
supplied, its required fields still receive full validation, including objects
inside lists. The generated schema preserves this distinction; required fields
with defaults may still be omitted.

`nullable` describes an accepted value, not permission to delete a key. On
backends using RFC 7396 merge patch, `null` removes the key; deleting a required
field can be rejected even if its type accepts null. Uniqueness and callbacks
still depend on server-side validation.

## Deployment

Swagger UI v5.35.0 and its license are embedded in the binary. Serving docs
requires no CDN or outgoing internet access. Relative spec, asset and server
URLs preserve a reverse proxy prefix when the proxy strips it before forwarding.

The framework reserves `/api/docs` (including its asset subtree),
`/api/openapi.json` and `/swagger`. Conflicting resource/custom patterns fail
registration with a clear panic, consistent with `http.ServeMux` registration.
A resource named `api` conflicts because its item route covers the docs paths;
use a different resource URL. These paths are reserved even when all resources
have `DisableSwagger: true`.
