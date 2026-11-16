// A standalone module demonstrating composition rather than router replacement.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/server"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/labstack/echo/v5"
)

// Mount preserves the full URL. app.Spec.URLPrefix removes /hyrule exactly
// once, keeping item lookups and relative Swagger URLs intact.
func Mount(router string, handler http.Handler) (http.Handler, error) {
	switch router {
	case "gin":
		r := gin.New()
		r.Any("/hyrule", gin.WrapH(handler))
		r.Any("/hyrule/*path", gin.WrapH(handler))
		return r, nil
	case "chi":
		r := chi.NewRouter()
		r.Handle("/hyrule", handler)
		r.Handle("/hyrule/*", handler)
		return r, nil
	case "echo":
		r := echo.New()
		r.Any("/hyrule", echo.WrapHandler(handler))
		r.Any("/hyrule/*", echo.WrapHandler(handler))
		return r, nil
	default:
		return nil, fmt.Errorf("unknown router %q (gin, chi, echo)", router)
	}
}
func build(ctx context.Context, path string) (http.Handler, func(), error) {
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	h, err := app.Build(ctx, app.Spec{Name: "router-example", Backend: app.BackendSQLite, URLPrefix: "hyrule", Setup: func(ctx context.Context, env *app.Env) error {
		resource := &rest.Resource{Name: "products", Schema: rest.Schema{"name": {Type: rest.TypeString, Required: true}}, ResourceMethods: []string{"GET", "POST"}, ItemMethods: []string{"GET", "PATCH", "PUT", "DELETE"}}
		adapter, err := restsqlite.NewAdapter(ctx, env.SQLite, "products", resource)
		if err != nil {
			return err
		}
		resource.Adapter = adapter
		env.API.Register(resource)
		return nil
	}}, app.Deps{SQLite: db, Config: config.Config{"auth": map[string]any{"enable": false}}})
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return h, func() { _ = db.Close() }, nil
}
func main() {
	router := os.Getenv("ROUTER")
	if router == "" {
		router = "chi"
	}
	handler, close, err := build(context.Background(), "hyrule.db")
	if err != nil {
		panic(err)
	}
	defer close()
	handler, err = Mount(router, handler)
	if err != nil {
		panic(err)
	}
	if err := server.Run(handler); err != nil {
		panic(err)
	}
}
