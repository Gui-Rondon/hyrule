package main

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/behaviors/searchsync"
	"github.com/Gui-Rondon/hyrule/platform/search"
	"github.com/Gui-Rondon/hyrule/rest"
)

func setup(_ context.Context, env *app.Env) error {
	res := &rest.Resource{
		Name:      "tags",
		ItemTitle: "tag",
		Store:     env.Store("tags"),
		Schema: rest.Schema{
			"name":    {Type: rest.TypeString, Required: true},
			"slug":    {Type: rest.TypeString, Required: true, Unique: true},
			"enabled": {Type: rest.TypeBoolean, Default: true},
		},
		AdditionalLookup:  &rest.AdditionalLookup{Field: "slug", Pattern: regexp.MustCompile(`^[\w-]+$`)},
		ResourceMethods:   []string{http.MethodGet, http.MethodPost},
		ItemMethods:       []string{http.MethodGet, http.MethodPatch, http.MethodPut},
		PublicMethods:     rest.PublicGET,
		PublicItemMethods: rest.PublicGET,
		QueryDenylist:     rest.DefaultQueryDenylist,
	}

	if _, ok := env.Config.Get("elasticsearch.hosts"); ok {
		es, err := search.FromConfig(env.Config)
		if err != nil {
			return err
		}
		res.Hooks.Use(searchsync.Hooks(es, env.Config.String("elasticsearch.index", "tags")))
		env.AddCheck(app.ElasticsearchCheck("elasticsearch_check", es))
	} else {
		slog.Warn("elasticsearch not configured, writes will not be indexed")
	}

	env.API.Register(res)
	return nil
}
