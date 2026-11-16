---
sidebar_position: 2
---
# Creating a platform project

The default `hyrule new my-api` creates one SQLite API. For an existing-style
multi-service Mongo project, explicitly select the platform layout:

```sh
go install github.com/Gui-Rondon/hyrule/cmd/hyrule@latest
hyrule new my-platform --layout=platform
cd my-platform
hyrule service create articles
go mod tidy
make env-up
make build SERVICE=articles
make run SERVICE=articles
```

The generated go.mod pins the framework to the installed CLI release. Development
CLI builds require `--framework-version=vX.Y.Z` or a published pseudo-version.
Do not run tidy before creating the first service: the empty project does not yet
import Hyrule, so tidy would remove the unused requirement.

`hyrule.json` records layout/backend. Platform projects contain go.mod, Makefile,
docker-compose.yml, README.md and services/. The single layout creates main.go
and resource files at the root instead. Existing projects with services/ and no
manifest retain legacy platform behavior. Files are never overwritten.

The next release containing this change provides the new default; older releases
continue generating platform projects. See [SQLite](../reference/sqlite.md) for
local experimentation and [compatibility](../reference/stability.md) for migration.
