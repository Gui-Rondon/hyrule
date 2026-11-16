package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouters(t *testing.T) {
	for _, router := range []string{"gin", "chi", "echo"} {
		t.Run(router, func(t *testing.T) {
			h, close, err := build(context.Background(), ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer close()
			h, err = Mount(router, h)
			if err != nil {
				t.Fatal(err)
			}
			inner := h
			h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Outer-Middleware", "yes")
				inner.ServeHTTP(w, r)
			})
			request := func(method, path, body, etag string, status int) map[string]any {
				t.Helper()
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				if etag != "" {
					req.Header.Set("If-Match", `"`+etag+`"`)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code != status || w.Header().Get("X-Outer-Middleware") != "yes" {
					t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
				}
				var result map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &result)
				return result
			}
			d := request("POST", "/hyrule/products", `{"name":"Sword"}`, "", 201)
			id, etag := d["_id"].(string), d["_etag"].(string)
			if request("GET", "/hyrule/products/"+id, "", "", 200)["name"] != "Sword" {
				t.Fatal("item lookup")
			}
			updated := request("PATCH", "/hyrule/products/"+id, `{"name":"Shield"}`, etag, 200)
			replaced := request("PUT", "/hyrule/products/"+id, `{"name":"Bow"}`, updated["_etag"].(string), 200)
			request("DELETE", "/hyrule/products/"+id, "", replaced["_etag"].(string), 204)
			request("GET", "/hyrule/products/"+id, "", "", 404)
			request("PUT", "/hyrule/products", "", "", 405)
			request("GET", "/hyrule/missing", "", "", 404)
			doc := request("GET", "/hyrule/api/openapi.json", "", "", 200)
			if doc["openapi"] != "3.1.0" {
				t.Fatal(doc)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/hyrule/api/docs/", nil))
			if w.Code != 200 || !strings.Contains(w.Body.String(), "../openapi.json") {
				t.Fatalf("Swagger %d %s", w.Code, w.Body)
			}
		})
	}
}
