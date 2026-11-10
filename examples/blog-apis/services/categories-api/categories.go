package main

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/internal/app"
	"github.com/Gui-Rondon/hyrule/internal/evecompat"
)

var slugRe = regexp.MustCompile(`^[\w-]+$`)

func setup(_ context.Context, env *app.Env) error {
	store := env.Store("categories")
	res := &evecompat.Resource{
		Name:      "categories",
		ItemTitle: "category",
		Store:     store,
		Schema: evecompat.Schema{
			"name":        {Type: evecompat.TypeString, Required: true},
			"slug":        {Type: evecompat.TypeString, Required: true, Unique: true, Regex: slugRe},
			"description": {Type: evecompat.TypeString},
			"parent_id":   {Type: evecompat.TypeObjectID, Nullable: true},
			"path":        {Type: evecompat.TypeString},
			"enabled":     {Type: evecompat.TypeBoolean, Default: true},
		},
		Datasource:        evecompat.Datasource{DefaultSort: bson.D{{Key: "name", Value: 1}}},
		AdditionalLookup:  &evecompat.AdditionalLookup{Field: "slug", Pattern: slugRe},
		ResourceMethods:   []string{http.MethodGet, http.MethodPost},
		ItemMethods:       []string{http.MethodGet, http.MethodPatch, http.MethodPut},
		PublicMethods:     evecompat.PublicGET,
		PublicItemMethods: evecompat.PublicGET,
		QueryDenylist:     evecompat.DefaultQueryDenylist,
	}
	res.Hooks.Use(pathHooks(store))

	env.API.Register(res)
	tree := treeHandler(store)
	env.API.Handle("GET /categories/tree", tree)
	env.API.Handle("GET /categories/tree/{path...}", tree)
	return nil
}

// category is a node of the tree, serving exactly the fields of the stored
// document plus items (its children).
type category = map[string]any

// loadAll reads every category through the Store interface — no
// *mongo.Collection anywhere in this file.
func loadAll(ctx context.Context, store evecompat.Store) ([]category, error) {
	return store.Find(ctx, bson.M{}, 0, 0, nil, nil)
}

// buildTree nests the flat list by parent_id, sorted by name.
func buildTree(docs []category) []category {
	byID := make(map[string]category, len(docs))
	for _, d := range docs {
		byID[idString(d["_id"])] = d
	}
	var roots []category
	for _, d := range docs {
		parent := idString(d["parent_id"])
		if parent == "" {
			roots = append(roots, d)
			continue
		}
		if p, ok := byID[parent]; ok {
			items, _ := p["items"].([]category)
			p["items"] = append(items, d)
		}
	}
	sortTree(roots)
	return roots
}

func sortTree(nodes []category) {
	sort.SliceStable(nodes, func(i, j int) bool {
		return strAt(nodes[i], "name") < strAt(nodes[j], "name")
	})
	for _, n := range nodes {
		if items, ok := n["items"].([]category); ok {
			sortTree(items)
		}
	}
}

// byPath walks the tree by slugs ("tech/programming/go").
func byPath(roots []category, path string) category {
	level := roots
	var found category
	for _, slug := range strings.Split(path, "/") {
		found = nil
		for _, n := range level {
			if strAt(n, "slug") == slug {
				found = n
				break
			}
		}
		if found == nil {
			return nil
		}
		level, _ = found["items"].([]category)
	}
	return found
}

// treeHandler serves GET /categories/tree[/path].
func treeHandler(store evecompat.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		docs, err := loadAll(r.Context(), store)
		if err != nil {
			evecompat.WriteError(w, r, err)
			return
		}
		roots := buildTree(docs)
		if path == "" {
			evecompat.WriteJSON(w, http.StatusOK, roots)
			return
		}
		node := byPath(roots, path)
		if node == nil {
			evecompat.WriteError(w, r, evecompat.ErrNotFound)
			return
		}
		evecompat.WriteJSON(w, http.StatusOK, node)
	})
}

// pathHooks keep "path" (parent's path + "/" + slug) correct on every
// write, reading the current tree inside the write transaction.
func pathHooks(store evecompat.Store) evecompat.Hooks {
	pathFor := func(ctx context.Context, parentID any, slug string) (string, error) {
		id := idString(parentID)
		if id == "" {
			return slug, nil
		}
		parent, err := store.FindOne(ctx, bson.M{"_id": parentID}, nil)
		if err != nil {
			return "", err
		}
		return strAt(parent, "path") + "/" + slug, nil
	}
	return evecompat.Hooks{
		OnInsert: []func(context.Context, []evecompat.Doc) error{
			func(ctx context.Context, docs []evecompat.Doc) error {
				for _, doc := range docs {
					p, err := pathFor(ctx, doc["parent_id"], strAt(doc, "slug"))
					if err != nil {
						return err
					}
					doc["path"] = p
				}
				return nil
			},
		},
		OnReplace: []func(context.Context, evecompat.Doc, evecompat.Doc) error{
			func(ctx context.Context, doc, _ evecompat.Doc) error {
				p, err := pathFor(ctx, doc["parent_id"], strAt(doc, "slug"))
				doc["path"] = p
				return err
			},
		},
		OnUpdate: []func(context.Context, evecompat.Doc, evecompat.Doc) error{
			func(ctx context.Context, updates, original evecompat.Doc) error {
				slug := strAt(original, "slug")
				if s, ok := updates["slug"].(string); ok {
					slug = s
				}
				parentID := original["parent_id"]
				if p, ok := updates["parent_id"]; ok {
					parentID = p
				}
				p, err := pathFor(ctx, parentID, slug)
				updates["path"] = p
				return err
			},
		},
	}
}

func idString(v any) string {
	switch id := v.(type) {
	case bson.ObjectID:
		return id.Hex()
	case nil:
		return ""
	default:
		return ""
	}
}

func strAt(doc category, key string) string {
	s, _ := doc[key].(string)
	return s
}
