package rest

import (
	"embed"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// swaggerAssets is the unmodified Swagger UI distribution; see its README.
//
//go:embed swagger-ui/swagger-ui-bundle.js swagger-ui/swagger-ui.css swagger-ui/LICENSE
var swaggerAssets embed.FS

const swaggerPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Hyrule API</title></head>
<body><div id="swagger-ui"></div><script>
const base = new URL(window.location.pathname.replace(/\/$/, '') + '/', window.location.href);
const css = document.createElement('link'); css.rel = 'stylesheet'; css.href = new URL('assets/swagger-ui.css', base); document.head.appendChild(css);
const script = document.createElement('script'); script.src = new URL('assets/swagger-ui-bundle.js', base);
script.onload = () => SwaggerUIBundle({url: new URL('../openapi.json', base).href, dom_id: '#swagger-ui', validatorUrl: null, persistAuthorization: false});
document.body.appendChild(script);
</script></body></html>`

func (a *API) registerSwagger() {
	a.mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, swaggerPage)
	})
	for _, name := range []string{"swagger-ui-bundle.js", "swagger-ui.css"} {
		data, _ := swaggerAssets.ReadFile("swagger-ui/" + name)
		contentType := "text/css; charset=utf-8"
		if strings.HasSuffix(name, ".js") {
			contentType = "text/javascript; charset=utf-8"
		}
		a.mux.HandleFunc("GET /api/docs/assets/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.Write(data)
		})
	}
	a.mux.HandleFunc("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, a.openAPIDocument())
	})
	a.mux.HandleFunc("GET /swagger", func(w http.ResponseWriter, r *http.Request) {
		// RequestURI retains a trailing slash stripped by the API router. Keep
		// Location relative so a reverse proxy's external prefix is preserved.
		target := "./api/docs"
		if original, err := url.ParseRequestURI(r.RequestURI); err == nil && strings.HasSuffix(original.Path, "/") {
			target = "../api/docs"
		}
		w.Header().Set("Location", target)
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
}

// checkSwaggerRoute reserves the documentation paths, including wildcard
// patterns that could shadow them. Broad parent handlers may coexist with docs.
func checkSwaggerRoute(pattern string) {
	parts := strings.Fields(pattern)
	if len(parts) == 0 {
		return
	} // ServeMux reports malformed patterns.
	route := parts[len(parts)-1]
	// ServeMux permits host-qualified patterns and percent-escaped segments.
	if slash := strings.IndexByte(route, '/'); slash >= 0 {
		route = route[slash:]
	}
	if decoded, err := url.PathUnescape(route); err == nil {
		route = decoded
	}
	route = path.Clean(route)
	for _, reserved := range []string{"/api/docs", "/api/openapi.json", "/swagger"} {
		if route == reserved || strings.HasPrefix(route, reserved+"/") || swaggerPatternMatches(route, reserved) {
			panic("rest: route " + pattern + " collides with reserved Swagger path " + reserved)
		}
	}
}

func swaggerPatternMatches(pattern, target string) bool {
	pp, tp := strings.Split(pattern, "/"), strings.Split(target, "/")
	for i, segment := range pp {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "...}") {
			return true
		}
		if i >= len(tp) {
			return false
		}
		if segment != tp[i] && !(strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") && segment != "{$}") {
			return false
		}
	}
	return len(pp) == len(tp)
}
