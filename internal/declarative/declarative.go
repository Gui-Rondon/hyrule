// Package declarative builds evecompat.Resource/Schema values from YAML,
// for the common case — a resource with no custom hooks or business logic
// (see internal/evecompat/service.go: settings-api, tags-api, authors-api
// in examples/blog-apis are entirely this shape already, hand-written).
//
// Two escapes of evecompat stay Go-only, because they carry arbitrary code
// rather than data: Field.Check and Resource.Validate. A resource that
// needs either keeps being described in Go, exactly as today — a
// declarative Resource and a hand-written one register through the same
// evecompat.API.Register, so a service can mix both.
//
// What this package does not cover: Resource.Datasource (bson.M filters
// and projections), Hooks of any kind, and the two escapes above. Those
// stay in Go.
package declarative

import (
	"fmt"
	"io"
	"net/http"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/Gui-Rondon/hyrule/internal/evecompat"
)

// File is a resources.yaml: one or more resources, registered independently
// of each other.
type File struct {
	Resources []Resource `yaml:"resources"`
}

// Resource mirrors the plain-data fields of evecompat.Resource.
type Resource struct {
	// Name is the DOMAIN key and, by default, the URL (evecompat.Resource.Name/URL).
	Name string `yaml:"name"`
	// Collection is the Mongo collection backing the resource (env.Store(Collection)).
	Collection string `yaml:"collection"`
	ItemTitle  string `yaml:"item_title"`

	Schema       map[string]Field `yaml:"schema"`
	AllowUnknown bool             `yaml:"allow_unknown"`

	AdditionalLookup *AdditionalLookup `yaml:"additional_lookup"`

	ResourceMethods   []string `yaml:"resource_methods"`
	ItemMethods       []string `yaml:"item_methods"`
	PublicMethods     []string `yaml:"public_methods"`
	PublicItemMethods []string `yaml:"public_item_methods"`

	Pagination *Pagination `yaml:"pagination"`
	SoftDelete bool        `yaml:"soft_delete"`

	CacheControl string `yaml:"cache_control"`
	CacheExpires int    `yaml:"cache_expires"`
}

// AdditionalLookup mirrors evecompat.AdditionalLookup (the Field/Pattern
// pair; Filter and PathLinks are Go-only, they carry a closure).
type AdditionalLookup struct {
	Field   string `yaml:"field"`
	Pattern string `yaml:"pattern"`
}

// Pagination mirrors evecompat.Pagination.
type Pagination struct {
	Default          int  `yaml:"default"`
	Limit            int  `yaml:"limit"`
	OptimizeForSpeed bool `yaml:"optimize_for_speed"`
	Disabled         bool `yaml:"disabled"`
}

// Field mirrors the plain-data fields of evecompat.Field. Check is Go-only
// (a closure), so it has no YAML counterpart.
type Field struct {
	Type       string   `yaml:"type"`
	Required   bool     `yaml:"required"`
	Nullable   bool     `yaml:"nullable"`
	Unique     bool     `yaml:"unique"`
	Default    any      `yaml:"default"`
	Allowed    []any    `yaml:"allowed"`
	MinLength  int      `yaml:"min_length"`
	MaxLength  int      `yaml:"max_length"`
	Min        *float64 `yaml:"min"`
	Max        *float64 `yaml:"max"`
	NotEmpty   bool     `yaml:"not_empty"`
	AllowEmpty bool     `yaml:"allow_empty"`
	Regex      string   `yaml:"regex"`

	Schema       map[string]Field `yaml:"schema"`
	AllowUnknown bool             `yaml:"allow_unknown"`
	Items        *Field           `yaml:"items"`
}

// Parse reads a resources.yaml.
func Parse(r io.Reader) (*File, error) {
	var f File
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("declarative: %w", err)
	}
	return &f, nil
}

// Build compiles r into an evecompat.Resource backed by store (typically
// env.Store(r.Collection)); the caller still does env.API.Register(res).
func (r Resource) Build(store evecompat.Store) (*evecompat.Resource, error) {
	schema, err := buildSchema(r.Schema)
	if err != nil {
		return nil, fmt.Errorf("resource %q: %w", r.Name, err)
	}
	res := &evecompat.Resource{
		Name:              r.Name,
		ItemTitle:         r.ItemTitle,
		Store:             store,
		Schema:            schema,
		AllowUnknown:      r.AllowUnknown,
		ResourceMethods:   methods(r.ResourceMethods),
		ItemMethods:       methods(r.ItemMethods),
		PublicMethods:     methods(r.PublicMethods),
		PublicItemMethods: methods(r.PublicItemMethods),
		SoftDelete:        r.SoftDelete,
		CacheControl:      r.CacheControl,
		CacheExpires:      r.CacheExpires,
	}
	if r.Pagination != nil {
		res.Pagination = evecompat.Pagination{
			Default: r.Pagination.Default, Limit: r.Pagination.Limit,
			OptimizeForSpeed: r.Pagination.OptimizeForSpeed, Disabled: r.Pagination.Disabled,
		}
	}
	if r.AdditionalLookup != nil {
		pattern, err := regexp.Compile(r.AdditionalLookup.Pattern)
		if err != nil {
			return nil, fmt.Errorf("resource %q: additional_lookup.pattern: %w", r.Name, err)
		}
		res.AdditionalLookup = &evecompat.AdditionalLookup{Field: r.AdditionalLookup.Field, Pattern: pattern}
	}
	return res, nil
}

// methods upper-cases the YAML method names into net/http constants (get ->
// GET), so resources.yaml reads naturally in either case.
func methods(names []string) []string {
	if names == nil {
		return nil
	}
	out := make([]string, len(names))
	for i, n := range names {
		switch n {
		case "GET", "get":
			out[i] = http.MethodGet
		case "POST", "post":
			out[i] = http.MethodPost
		case "PATCH", "patch":
			out[i] = http.MethodPatch
		case "PUT", "put":
			out[i] = http.MethodPut
		case "DELETE", "delete":
			out[i] = http.MethodDelete
		default:
			out[i] = n
		}
	}
	return out
}

func buildSchema(fields map[string]Field) (evecompat.Schema, error) {
	if fields == nil {
		return nil, nil
	}
	schema := make(evecompat.Schema, len(fields))
	for name, f := range fields {
		built, err := f.build()
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		schema[name] = built
	}
	return schema, nil
}

// validTypes are the FieldType values evecompat understands; an empty type
// accepts any value, so it's valid too (evecompat/schema.go).
var validTypes = map[string]bool{
	"": true, string(evecompat.TypeString): true, string(evecompat.TypeBoolean): true,
	string(evecompat.TypeInteger): true, string(evecompat.TypeNumber): true, string(evecompat.TypeDict): true,
	string(evecompat.TypeList): true, string(evecompat.TypeObjectID): true, string(evecompat.TypeDatetime): true,
}

func (f Field) build() (evecompat.Field, error) {
	if !validTypes[f.Type] {
		return evecompat.Field{}, fmt.Errorf("type: unknown %q", f.Type)
	}
	built := evecompat.Field{
		Type:         evecompat.FieldType(f.Type),
		Required:     f.Required,
		Nullable:     f.Nullable,
		Unique:       f.Unique,
		Default:      f.Default,
		Allowed:      f.Allowed,
		MinLength:    f.MinLength,
		MaxLength:    f.MaxLength,
		Min:          f.Min,
		Max:          f.Max,
		NotEmpty:     f.NotEmpty,
		AllowEmpty:   f.AllowEmpty,
		AllowUnknown: f.AllowUnknown,
	}
	if f.Regex != "" {
		re, err := regexp.Compile(f.Regex)
		if err != nil {
			return evecompat.Field{}, fmt.Errorf("regex: %w", err)
		}
		built.Regex = re
	}
	if f.Schema != nil {
		schema, err := buildSchema(f.Schema)
		if err != nil {
			return evecompat.Field{}, err
		}
		built.Schema = schema
	}
	if f.Items != nil {
		items, err := f.Items.build()
		if err != nil {
			return evecompat.Field{}, fmt.Errorf("items: %w", err)
		}
		built.Items = &items
	}
	return built, nil
}
