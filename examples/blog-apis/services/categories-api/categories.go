package main

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/rest"
)

var slugRe = regexp.MustCompile(`^[\w-]+$`)

func setup(_ context.Context, env *app.Env) error {
	store := env.Store("categories")
	res := &rest.Resource{
		Name:      "categories",
		ItemTitle: "category",
		Store:     store,
		Schema: rest.Schema{
			"name":        {Type: rest.TypeString, Required: true},
			"slug":        {Type: rest.TypeString, Required: true, Unique: true, Regex: slugRe},
			"description": {Type: rest.TypeString},
			"parent_id":   {Type: rest.TypeObjectID, Nullable: true},
			"path":        {Type: rest.TypeString},
			"enabled":     {Type: rest.TypeBoolean, Default: true},
		},
		Datasource:        rest.Datasource{DefaultSort: bson.D{{Key: "name", Value: 1}}},
		AdditionalLookup:  &rest.AdditionalLookup{Field: "slug", Pattern: slugRe},
		ResourceMethods:   []string{http.MethodGet, http.MethodPost},
		ItemMethods:       []string{http.MethodGet, http.MethodPatch, http.MethodPut},
		PublicMethods:     rest.PublicGET,
		PublicItemMethods: rest.PublicGET,
		QueryDenylist:     rest.DefaultQueryDenylist,
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
func loadAll(ctx context.Context, store rest.Store) ([]category, error) {
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
func treeHandler(store rest.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		docs, err := loadAll(r.Context(), store)
		if err != nil {
			rest.WriteError(w, r, err)
			return
		}
		roots := buildTree(docs)
		if path == "" {
			rest.WriteJSON(w, http.StatusOK, roots)
			return
		}
		node := byPath(roots, path)
		if node == nil {
			rest.WriteError(w, r, rest.ErrNotFound)
			return
		}
		rest.WriteJSON(w, http.StatusOK, node)
	})
}

// pathHooks keep "path" (parent's path + "/" + slug) correct on every
// write, reading the current tree inside the write transaction.
func pathHooks(store rest.Store) rest.Hooks {
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
	return rest.Hooks{
		OnInsert: []func(context.Context, []rest.Doc) error{
			func(ctx context.Context, docs []rest.Doc) error {
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
		OnReplace: []func(context.Context, rest.Doc, rest.Doc) error{
			func(ctx context.Context, doc, _ rest.Doc) error {
				p, err := pathFor(ctx, doc["parent_id"], strAt(doc, "slug"))
				doc["path"] = p
				return err
			},
		},
		OnUpdate: []func(context.Context, rest.Doc, rest.Doc) error{
			func(ctx context.Context, updates, original rest.Doc) error {
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
