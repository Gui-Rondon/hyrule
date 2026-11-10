package main

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/Gui-Rondon/hyrule/internal/app"
	"github.com/Gui-Rondon/hyrule/internal/behaviors/searchsync"
	"github.com/Gui-Rondon/hyrule/internal/evecompat"
	"github.com/Gui-Rondon/hyrule/internal/platform/search"
)

func setup(_ context.Context, env *app.Env) error {
	res := &evecompat.Resource{
		Name:      "tags",
		ItemTitle: "tag",
		Store:     env.Store("tags"),
		Schema: evecompat.Schema{
			"name":    {Type: evecompat.TypeString, Required: true},
			"slug":    {Type: evecompat.TypeString, Required: true, Unique: true},
			"enabled": {Type: evecompat.TypeBoolean, Default: true},
		},
		AdditionalLookup:  &evecompat.AdditionalLookup{Field: "slug", Pattern: regexp.MustCompile(`^[\w-]+$`)},
		ResourceMethods:   []string{http.MethodGet, http.MethodPost},
		ItemMethods:       []string{http.MethodGet, http.MethodPatch, http.MethodPut},
		PublicMethods:     evecompat.PublicGET,
		PublicItemMethods: evecompat.PublicGET,
		QueryDenylist:     evecompat.DefaultQueryDenylist,
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
