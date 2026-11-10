package main

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/behaviors/audit"
	"github.com/Gui-Rondon/hyrule/behaviors/searchsync"
	"github.com/Gui-Rondon/hyrule/platform/search"
	"github.com/Gui-Rondon/hyrule/rest"
)

var handle = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func setup(_ context.Context, env *app.Env) error {
	res := &rest.Resource{
		Name:      "authors",
		ItemTitle: "author",
		Store:     env.Store("authors"),
		Schema: rest.Schema{
			"name":      {Type: rest.TypeString, Required: true},
			"email":     {Type: rest.TypeString, Unique: true, Regex: regexp.MustCompile(`^.+\@.+\..+$`)},
			"bio":       {Type: rest.TypeString},
			"photo":     {Type: rest.TypeString},
			"twitter":   {Type: rest.TypeString, Regex: handle},
			"instagram": {Type: rest.TypeString, Regex: handle},
		},
		ResourceMethods:   []string{http.MethodGet, http.MethodPost},
		ItemMethods:       []string{http.MethodGet, http.MethodPatch, http.MethodPut},
		PublicMethods:     rest.PublicGET,
		PublicItemMethods: rest.PublicGET,
		QueryDenylist:     rest.DefaultQueryDenylist,
		CacheControl:      "max-age=20",
		CacheExpires:      20,
	}
	res.Hooks.Use(audit.Hooks())

	if _, ok := env.Config.Get("elasticsearch.hosts"); ok {
		es, err := search.FromConfig(env.Config)
		if err != nil {
			return err
		}
		res.Hooks.Use(searchsync.Hooks(es, env.Config.String("elasticsearch.index", "authors")))
		env.AddCheck(app.ElasticsearchCheck("elasticsearch_check", es))
	} else {
		slog.Warn("elasticsearch not configured, writes will not be indexed")
	}

	env.API.Register(res)
	return nil
}
