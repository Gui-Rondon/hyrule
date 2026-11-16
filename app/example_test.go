package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
)

func ExampleBuild() {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	spec := app.Spec{Name: "example", Backend: app.BackendSQLite, Setup: func(ctx context.Context, env *app.Env) error {
		resource := &rest.Resource{Name: "products", Schema: rest.Schema{"name": {Type: rest.TypeString, Required: true}}}
		adapter, err := restsqlite.NewAdapter(ctx, env.SQLite, "products", resource)
		if err != nil {
			return err
		}
		resource.Adapter = adapter
		env.API.Register(resource)
		return nil
	}}
	handler, err := app.Build(ctx, spec, app.Deps{SQLite: db, Config: config.Config{"auth": map[string]any{"enable": false}}})
	if err != nil {
		panic(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/products", nil))
	fmt.Println(response.Code)
	// Output: 200
}

func ExampleSpec() {
	spec := app.Spec{Name: "products", Version: "dev", Setup: func(_ context.Context, env *app.Env) error {
		env.API.Register(&rest.Resource{Name: "products", Store: env.Store("products"), Schema: rest.Schema{"name": {Type: rest.TypeString, Required: true}}})
		return nil
	}}
	// app.Main(spec) connects to MongoDB and serves until shutdown.
	fmt.Println(spec.Name, spec.Version)
	// Output: products dev
}
