package rest

import (
	"encoding/hex"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// openAPIDocument uses the same defaulted resources as the handlers. Like
// Register itself, resource definitions must not be mutated while serving.
func (a *API) openAPIDocument() map[string]any {
	paths, schemas := map[string]any{}, map[string]any{}
	for _, svc := range a.services {
		r := svc.res
		if r.DisableSwagger {
			continue
		}
		// Hex is injective even for names containing slashes or punctuation, and
		// is safe in both component keys and JSON pointers.
		key := "Resource_" + hex.EncodeToString([]byte(r.Name+"\x00"+r.URL))
		schemas[key+"Input"] = openAPIObject(r.Schema, r.AllowUnknown, false)
		schemas[key+"Patch"] = openAPIObject(r.Schema, r.AllowUnknown, true)
		output := openAPIObject(r.Schema, true, true)
		props := output["properties"].(map[string]any)
		for k, v := range openAPIMeta(r) {
			props[k] = v
		}
		schemas[key+"Document"] = output
		schemas[key+"WriteResult"] = map[string]any{"type": "object", "properties": openAPIMeta(r)}
		collection, item := map[string]any{}, map[string]any{}
		for _, m := range r.ResourceMethods {
			if m == http.MethodGet || m == http.MethodPost {
				collection[strings.ToLower(m)] = a.openAPIOperation(r, key, m, false)
			}
		}
		methods := slices.Clone(r.ItemMethods)
		// Additional lookups allow GET even when ID reads are disabled.
		if r.AdditionalLookup != nil && !slices.Contains(methods, http.MethodGet) {
			methods = append(methods, http.MethodGet)
		}
		for _, m := range methods {
			switch m {
			case http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete:
				item[strings.ToLower(m)] = a.openAPIOperation(r, key, m, true)
			}
		}
		if len(collection) > 0 {
			paths["/"+r.URL] = collection
		}
		if len(item) > 0 {
			paths["/"+r.URL+"/{lookup}"] = item
		}
	}
	components := map[string]any{"schemas": schemas}
	if a.opts.Auth != nil && a.opts.Auth.Enabled() {
		components["securitySchemes"] = map[string]any{"bearerAuth": map[string]any{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"}}
	}
	return map[string]any{
		"openapi": "3.1.0", "info": map[string]any{"title": "Hyrule API", "version": "1.0.0"},
		// Relative to /api/openapi.json, preserving any proxy prefix.
		"servers": []any{map[string]any{"url": ".."}}, "paths": paths, "components": components,
	}
}

func openAPIRef(key string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + key}
}
func openAPIContent(schema any) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": schema}}
}
func openAPIResponse(description string, schema any) map[string]any {
	r := map[string]any{"description": description}
	if schema != nil {
		r["content"] = openAPIContent(schema)
	}
	return r
}
func openAPIParameter(name, in, description string, schema any, required bool) map[string]any {
	return map[string]any{"name": name, "in": in, "description": description, "schema": schema, "required": required}
}
func openAPIString() map[string]any { return map[string]any{"type": "string"} }

func (a *API) openAPIOperation(r *Resource, key, method string, item bool) map[string]any {
	scope := "collection"
	public := r.PublicMethods
	params := []any{}
	description := "Validation callbacks and hooks may impose additional server-side rules."
	if item {
		scope, public = "item", r.PublicItemMethods
		lookup := "Document ID as a string."
		if r.AdditionalLookup != nil {
			lookup += " GET also accepts the additional lookup field " + r.AdditionalLookup.Field + "."
			if !slices.Contains(r.ItemMethods, http.MethodGet) {
				lookup += " GET is enabled only for the additional lookup, not IDs."
			}
			if r.AdditionalLookup.Pattern != nil {
				lookup += " Additional lookup pattern: " + r.AdditionalLookup.Pattern.String()
			}
		}
		params = append(params, openAPIParameter("lookup", "path", lookup, openAPIString(), true))
		if method != http.MethodGet {
			description += " Writes require a document ID; additional lookups are read-only."
		}
	}
	responses := map[string]any{}
	// Error envelope varies for validation and bulk inserts; allow the actual
	// _error/_issues/_items forms instead of claiming one rigid error shape.
	errSchema := map[string]any{"type": "object", "properties": map[string]any{
		"_status": openAPIString(), "_error": map[string]any{"type": "object"}, "_issues": map[string]any{"type": "object"}, "_items": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
	}}
	for _, status := range []int{400, 404, 409, 422, 500, 503} {
		responses[strconv.Itoa(status)] = openAPIResponse(http.StatusText(status), errSchema)
	}
	if a.opts.Auth != nil && a.opts.Auth.Enabled() && !slices.Contains(public, method) {
		responses["403"] = openAPIResponse("Missing or invalid credentials. HTML when HTMLForbidden is enabled.", errSchema)
	}
	switch method {
	case http.MethodGet:
		params = append(params, openAPIParameter("projection", "query", "JSON field projection.", openAPIString(), false))
		var schema any = openAPIRef(key + "Document")
		if !item {
			params = append(params,
				openAPIParameter("where", "query", "JSON filter using the resource backend's query language.", openAPIString(), false),
				openAPIParameter("sort", "query", "Comma-separated fields; prefix descending fields with -.", openAPIString(), false))
			if !r.Pagination.Disabled {
				params = append(params,
					openAPIParameter("page", "query", "Page number.", map[string]any{"type": "integer", "minimum": 1, "default": 1}, false),
					openAPIParameter("max_results", "query", "Maximum documents per page.", map[string]any{"type": "integer", "minimum": 1, "default": r.Pagination.Default, "maximum": r.Pagination.Limit}, false))
			}
			if r.SoftDelete {
				params = append(params, openAPIParameter("show_deleted", "query", "Presence includes soft-deleted documents, regardless of value.", openAPIString(), false))
			}
			properties := map[string]any{"_items": map[string]any{"type": "array", "items": schema}, "_links": map[string]any{"type": "object"}}
			if !r.Pagination.Disabled {
				meta := map[string]any{"page": map[string]any{"type": "integer"}, "max_results": map[string]any{"type": "integer"}}
				if !r.Pagination.OptimizeForSpeed {
					meta["total"] = map[string]any{"type": "integer"}
				}
				properties["_meta"] = map[string]any{"type": "object", "properties": meta}
			}
			schema = map[string]any{"type": "object", "properties": properties}
		} else {
			params = append(params, openAPIParameter("If-None-Match", "header", "Return 304 when the ETag matches.", openAPIString(), false), openAPIParameter("If-Modified-Since", "header", "HTTP date for conditional reads.", openAPIString(), false))
			responses["304"] = openAPIResponse("Not modified", nil)
		}
		response := openAPIResponse("OK", schema)
		headers := map[string]any{"Last-Modified": map[string]any{"schema": openAPIString()}}
		if item {
			headers["ETag"] = map[string]any{"schema": openAPIString()}
		} else if !r.Pagination.OptimizeForSpeed || r.Pagination.Disabled {
			headers["X-Total-Count"] = map[string]any{"schema": map[string]any{"type": "integer"}}
		}
		response["headers"] = headers
		responses["200"] = response
	case http.MethodPost:
		schema := map[string]any{"anyOf": []any{openAPIRef(key + "WriteResult"), map[string]any{"type": "object", "properties": map[string]any{"_status": openAPIString(), "_items": map[string]any{"type": "array", "items": openAPIRef(key + "WriteResult")}}}}}
		responses["201"] = openAPIResponse("Created (single document or batch)", schema)
	case http.MethodPatch, http.MethodPut, http.MethodDelete:
		params = append(params, openAPIParameter("If-Match", "header", "Current ETag, or * where supported.", openAPIString(), true))
		responses["412"] = openAPIResponse("ETag mismatch", errSchema)
		responses["428"] = openAPIResponse("If-Match required", errSchema)
		if method == http.MethodDelete {
			responses["204"] = openAPIResponse("Deleted", nil)
		} else {
			response := openAPIResponse("Updated", openAPIRef(key+"WriteResult"))
			response["headers"] = map[string]any{"ETag": map[string]any{"schema": openAPIString()}}
			responses["200"] = response
		}
	}
	op := map[string]any{"tags": []string{r.Name}, "operationId": key + "_" + scope + "_" + strings.ToLower(method), "summary": method + " " + r.URL + " (" + scope + ")", "description": description, "parameters": params, "responses": responses, "security": []any{}}
	if a.opts.Auth != nil && a.opts.Auth.Enabled() && !slices.Contains(public, method) {
		op["security"] = []any{map[string]any{"bearerAuth": []string{}}}
	}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		var schema any = openAPIRef(key + "Input")
		if method == http.MethodPost {
			schema = map[string]any{"oneOf": []any{schema, map[string]any{"type": "array", "items": schema}}}
		}
		if method == http.MethodPatch {
			schema = openAPIRef(key + "Patch")
		}
		content := openAPIContent(schema)
		if method == http.MethodPatch {
			if r.RequireMergePatchContentType {
				delete(content, "application/json")
				responses["415"] = openAPIResponse("Use application/merge-patch+json", errSchema)
			}
			content[mergePatchContentType] = map[string]any{"schema": schema}
		}
		op["requestBody"] = map[string]any{"required": true, "content": content}
	}
	return op
}

func openAPIMeta(r *Resource) map[string]any {
	props := map[string]any{"_id": openAPIString(), "_etag": openAPIString(), "_created": map[string]any{"type": "string", "description": "UTC date in Eve DATE_FORMAT: " + DateFormat}, "_updated": map[string]any{"type": "string", "description": "UTC date in Eve DATE_FORMAT: " + DateFormat}, "_links": map[string]any{"type": "object"}, "_status": openAPIString()}
	if r.SoftDelete {
		props["_deleted"] = map[string]any{"type": "boolean"}
	}
	return props
}

func openAPIObject(schema Schema, unknown, partial bool) map[string]any {
	props := map[string]any{}
	required := []string{}
	for name, field := range schema {
		props[name] = openAPIField(field, partial)
		if field.Required && field.Default == nil && !partial {
			required = append(required, name)
		}
	}
	out := map[string]any{"type": "object", "properties": props, "additionalProperties": unknown}
	if len(required) > 0 {
		sort.Strings(required)
		out["required"] = required
	}
	return out
}

func openAPIField(f Field, partial bool) map[string]any {
	out := map[string]any{}
	typ := string(f.Type)
	switch f.Type {
	case TypeDict:
		out = openAPIObject(f.Schema, f.AllowUnknown || f.Schema == nil, partial)
		typ = "object"
	case TypeList:
		typ = "array"
		items := map[string]any{}
		if f.Items != nil {
			items = openAPIField(*f.Items, partial)
		}
		if len(f.Allowed) > 0 {
			items["enum"] = Render(f.Allowed)
		}
		out["items"] = items
	case TypeObjectID:
		typ = "string"
		out["pattern"] = "^[0-9a-fA-F]{24}$"
	case TypeDatetime:
		typ = "string"
		out["description"] = "Date in Eve DATE_FORMAT: " + DateFormat
	}
	if typ == "" && !f.Nullable {
		out["not"] = map[string]any{"type": "null"}
	}
	if typ != "" {
		if f.Nullable {
			out["type"] = []string{typ, "null"}
		} else {
			out["type"] = typ
		}
	}
	if len(f.Allowed) > 0 && f.Type != TypeList {
		out["enum"] = Render(f.Allowed)
	}
	if f.Nullable {
		if values, ok := out["enum"].([]any); ok && !slices.Contains(values, nil) {
			out["enum"] = append(slices.Clone(values), nil)
		}
	}
	if f.Default != nil {
		out["default"] = Render(f.Default)
	}
	if f.Type == TypeString || f.Type == TypeList {
		minKey, maxKey := "minLength", "maxLength"
		if f.Type == TypeList {
			minKey, maxKey = "minItems", "maxItems"
		}
		min := f.MinLength
		if f.NotEmpty && min < 1 {
			min = 1
		}
		if min > 0 {
			out[minKey] = min
		}
		if f.MaxLength > 0 {
			out[maxKey] = f.MaxLength
		}
	}
	if f.Min != nil {
		out["minimum"] = *f.Min
	}
	if f.Max != nil {
		out["maximum"] = *f.Max
	}
	// Go/RE2 and JSON Schema regular expressions differ. Preserve the exact
	// server rule as an extension instead of emitting an invalid JS pattern.
	if f.Regex != nil {
		out["x-hyrule-regex"] = f.Regex.String()
	}
	if f.Unique {
		out["x-hyrule-unique"] = true
	}
	if f.Check != nil {
		out["x-hyrule-custom-validation"] = true
	}
	if f.AllowEmpty {
		out["x-hyrule-allow-empty"] = true
		// empty: true bypasses scalar/list rules only for the empty value;
		// keep constraints on all other values. empty: false takes priority.
		if !f.NotEmpty && (f.Type == TypeString || f.Type == TypeList) {
			var empty any = ""
			if f.Type == TypeList {
				empty = []any{}
			}
			return map[string]any{"anyOf": []any{out, map[string]any{"const": empty}}}
		}
	}
	return out
}
