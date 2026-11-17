package taglookup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/rest"
)

func httpPilot(t *testing.T) (*rig, http.Handler, string, string) {
	t.Helper()
	r := setup(t, nil)
	key := rsaKey(t)
	id := identity(t, key)
	h, err := NewHTTPHandler(r.d.Service, id, r.d.Audit)
	if err != nil {
		t.Fatal(err)
	}
	allowed := signed(t, key, "key-1", tokenClaims())
	claims := tokenClaims()
	claims["scope"] = "other:read"
	forbidden := signed(t, key, "key-1", claims)
	return r, h, allowed, forbidden
}
func TestHTTPAuthenticationInputAndDisclosure(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, media, credential string
		status, reads                               int
	}{
		{"describe", "GET", HTTPPath, "", "", "allowed", 200, 0},
		{"lookup", "POST", HTTPPath, `{"slug":"go"}`, "application/json", "allowed", 200, 1},
		{"absent", "POST", HTTPPath, `{"slug":"missing"}`, "application/json", "allowed", 200, 1},
		{"disabled", "POST", HTTPPath, `{"slug":"hidden"}`, "application/json", "allowed", 200, 1},
		{"missing token", "POST", HTTPPath, `{"slug":"go"}`, "application/json", "", 401, 0},
		{"bad token", "GET", HTTPPath, "", "", "bad", 401, 0},
		{"describe forbidden", "GET", HTTPPath, "", "", "forbidden", 403, 0},
		{"lookup forbidden", "POST", HTTPPath, `{"slug":"go"}`, "application/json", "forbidden", 403, 0},
		{"bad JSON", "POST", HTTPPath, `{`, "application/json", "allowed", 400, 0},
		{"duplicate key", "POST", HTTPPath, `{"slug":"go","slug":"hidden"}`, "application/json", "allowed", 400, 0},
		{"query injection", "POST", HTTPPath + "?where=anything", `{"slug":"go"}`, "application/json", "allowed", 400, 0},
		{"query credentials", "GET", HTTPPath + "?token=secret", "", "", "", 401, 0},
		{"body on GET", "GET", HTTPPath, `{"slug":"go"}`, "application/json", "allowed", 400, 0},
		{"wrong media", "POST", HTTPPath, `{"slug":"go"}`, "text/plain", "allowed", 415, 0},
		{"oversize", "POST", HTTPPath, strings.Repeat(" ", MaxInput+1), "application/json", "allowed", 413, 0},
		{"unauthenticated oversize", "POST", HTTPPath, strings.Repeat(" ", MaxInput+1), "application/json", "", 401, 0},
		{"unknown path", "GET", "/tools/other", "", "", "allowed", 404, 0},
		{"write method", "DELETE", HTTPPath, "", "", "allowed", 405, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, h, allowed, forbidden := httpPilot(t)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.media != "" {
				req.Header.Set("Content-Type", tc.media)
			}
			token := tc.credential
			switch token {
			case "allowed":
				token = allowed
			case "forbidden":
				token = forbidden
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status || r.a.reads != tc.reads {
				t.Fatalf("status=%d reads=%d: %s", w.Code, r.a.reads, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Vary") != "Authorization" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("unsafe response headers")
			}
			body := w.Body.String()
			if strings.Contains(body, allowed) || strings.Contains(body, "_id") || strings.Contains(body, "secret") {
				t.Fatal("private data leaked")
			}
			if tc.name == "describe" && !strings.Contains(body, `"read_only":true`) {
				t.Fatal("missing authorized descriptor")
			}
			if tc.name == "lookup" && body != `{"found":true,"tag":{"name":"Go","slug":"go","enabled":true}}` {
				t.Fatalf("wrong lookup: %s", body)
			}
			if (tc.name == "absent" || tc.name == "disabled") && body != `{"found":false}` {
				t.Fatalf("wrong not-found shape: %s", body)
			}
			if tc.status == 401 && w.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing bearer challenge")
			}
			if tc.status == 405 && w.Header().Get("Allow") != "GET, POST" {
				t.Fatal("wrong allowed methods")
			}
			if tc.status != 404 && tc.status != 405 && len(r.records) != 1 {
				t.Fatal("invocation was not audited")
			}
		})
	}
}
func TestHTTPCredentialsNeverUseCookiesOrInheritedToken(t *testing.T) {
	r, h, token, _ := httpPilot(t)
	for _, configure := range []func(*http.Request){
		func(req *http.Request) { req.AddCookie(&http.Cookie{Name: "token", Value: token}) },
		func(req *http.Request) {
			req.Header.Add("Authorization", "Bearer "+token)
			req.Header.Add("Authorization", "Bearer "+token)
		},
		func(req *http.Request) { req.Header.Set("Authorization", "Basic "+token) },
	} {
		req := httptest.NewRequest("GET", HTTPPath, nil).WithContext(WithToken(context.Background(), token))
		configure(req)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("credential bypass: %d %s", w.Code, w.Body)
		}
	}
	if r.a.reads != 0 {
		t.Fatal("unverified credentials accessed storage")
	}
}
func TestHTTPFailureMappingAndUnknownLengthLimit(t *testing.T) {
	r, h, token, _ := httpPilot(t)
	req := httptest.NewRequest("POST", HTTPPath, strings.NewReader(strings.Repeat(" ", MaxInput+1)))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 413 || r.a.reads != 0 {
		t.Fatalf("unknown-length limit: %d %s", w.Code, w.Body)
	}
	r.a.find = func(context.Context) ([]rest.Doc, error) { return nil, errors.New("secret DSN") }
	req = httptest.NewRequest("POST", HTTPPath, strings.NewReader(`{"slug":"go"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("database failure leaked: %d %s", w.Code, w.Body)
	}
	key := rsaKey(t)
	id := identity(t, key)
	h, err := NewHTTPHandler(r.d.Service, id, func(context.Context, Record) error { return errors.New("secret audit") })
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("GET", HTTPPath, nil)
	req.Header.Set("Authorization", "Bearer "+signed(t, key, "key-1", tokenClaims()))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 503 || strings.Contains(w.Body.String(), "tags.lookup") {
		t.Fatalf("audit failure exposed descriptor: %d %s", w.Code, w.Body)
	}
}
func TestLoopbackHTTPWithRealJWTAndPersistentAudit(t *testing.T) {
	r := setup(t, nil)
	r.d.Service.Resource().Adapter = r.a.Adapter
	audit, err := OpenSQLiteAudit(context.Background(), filepath.Join(t.TempDir(), "http-audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	key := rsaKey(t)
	id, err := LoadJWTIdentity(JWTFileConfig{Issuer: testIssuer, Audience: testAudience, Scope: testScope, KeyID: "key-1", PublicKeyFile: publicKeyFile(t, &key.PublicKey)})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(r.d.Service, id, audit.Write)
	if err != nil {
		t.Fatal(err)
	}
	config, err := NewLocalHTTPServer("127.0.0.1:0", handler)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Config = config
	server.Start()
	defer server.Close()
	token := signed(t, key, "key-1", tokenClaims())
	request, err := http.NewRequest("POST", server.URL+HTTPPath, strings.NewReader(`{"slug":"go"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), `"slug":"go"`) {
		t.Fatalf("HTTP lookup: %d %s %v", response.StatusCode, data, err)
	}
	// A partial body must not keep the connection/query blocked indefinitely.
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{", HTTPPath, token)
	if err != nil {
		t.Fatal(err)
	}
	slow, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Body.Close()
	if slow.StatusCode != 408 && slow.StatusCode != 504 {
		t.Fatalf("slow body status %d", slow.StatusCode)
	}
	var count int
	if err = audit.db.QueryRow("SELECT count(*) FROM tags_lookup_audit").Scan(&count); err != nil || count != 2 {
		t.Fatalf("missing network audit: %d %v", count, err)
	}
}
func TestHTTPRequiresTrustAndExplicitLoopback(t *testing.T) {
	r := setup(t, nil)
	if _, err := NewHTTPHandler(r.d.Service, nil, r.d.Audit); err == nil {
		t.Fatal("missing JWT trust accepted")
	}
	if _, err := NewHTTPHandler(r.d.Service, &JWTIdentity{}, r.d.Audit); err == nil {
		t.Fatal("empty JWT trust accepted")
	}
	for _, address := range []string{"", ":8080", "0.0.0.0:8080", "localhost:8080", "127.0.0.1:-1", "127.0.0.1:65536"} {
		if _, err := NewLocalHTTPServer(address, http.NotFoundHandler()); err == nil {
			t.Fatalf("unsafe bind accepted: %q", address)
		}
	}
	if _, err := NewLocalHTTPServer("127.0.0.1:0", nil); err == nil {
		t.Fatal("missing handler accepted")
	}
}

func TestHTTPDeadlineCancellationAndInvalidOutput(t *testing.T) {
	r, h, token, _ := httpPilot(t)
	request := func(ctx context.Context) *http.Request {
		req := httptest.NewRequest("POST", HTTPPath, strings.NewReader(`{"slug":"go"}`)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		return req
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request(canceled))
	if w.Code != 408 || r.a.reads != 0 {
		t.Fatalf("canceled request: %d %s", w.Code, w.Body)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request(expired))
	if w.Code != 504 || r.a.reads != 0 {
		t.Fatalf("expired request: %d %s", w.Code, w.Body)
	}
	r.a.find = func(context.Context) ([]rest.Doc, error) {
		return []rest.Doc{{"name": "Go", "slug": "go", "enabled": false}}, nil
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request(context.Background()))
	if w.Code != 500 || strings.Contains(w.Body.String(), `"tag"`) {
		t.Fatalf("invalid output leaked: %d %s", w.Code, w.Body)
	}
}
