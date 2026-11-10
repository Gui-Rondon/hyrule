package main

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/Gui-Rondon/hyrule/internal/app"
	"github.com/Gui-Rondon/hyrule/internal/behaviors/audit"
	"github.com/Gui-Rondon/hyrule/internal/behaviors/searchsync"
	"github.com/Gui-Rondon/hyrule/internal/evecompat"
	"github.com/Gui-Rondon/hyrule/internal/platform/search"
)

var handle = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func setup(_ context.Context, env *app.Env) error {
	res := &evecompat.Resource{
		Name:      "authors",
		ItemTitle: "author",
		Store:     env.Store("authors"),
		Schema: evecompat.Schema{
			"name":      {Type: evecompat.TypeString, Required: true},
			"email":     {Type: evecompat.TypeString, Unique: true, Regex: regexp.MustCompile(`^.+\@.+\..+$`)},
			"bio":       {Type: evecompat.TypeString},
			"photo":     {Type: evecompat.TypeString},
			"twitter":   {Type: evecompat.TypeString, Regex: handle},
			"instagram": {Type: evecompat.TypeString, Regex: handle},
		},
		ResourceMethods:   []string{http.MethodGet, http.MethodPost},
		ItemMethods:       []string{http.MethodGet, http.MethodPatch, http.MethodPut},
		PublicMethods:     evecompat.PublicGET,
		PublicItemMethods: evecompat.PublicGET,
		QueryDenylist:     evecompat.DefaultQueryDenylist,
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
