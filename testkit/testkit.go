// Package testkit has the helpers shared by the service tests: an isolated
// Mongo database, an Elasticsearch recorder, token signing and an HTTP client
// for handlers.
//
// Tests that need Mongo are skipped unless MONGO_TEST_URI is set, e.g.
// mongodb://localhost:27018/?directConnection=true (make env-up).
package testkit

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/platform/auth"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/platform/mongodb/mongotest"
	"github.com/Gui-Rondon/hyrule/rest"
)

var (
	clientOnce sync.Once
	client     *mongo.Client
	clientErr  error
)

// MongoClient returns a client shared by the tests of the package.
func MongoClient(t testing.TB) *mongo.Client {
	t.Helper()
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	clientOnce.Do(func() {
		client, clientErr = mongo.Connect(options.Client().ApplyURI(uri))
		if clientErr == nil {
			clientErr = mongotest.Prepare(context.Background(), client)
		}
	})
	if clientErr != nil {
		t.Fatal(clientErr)
	}
	return client
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// Store wraps the named collection of Mongo(t) for rest.Resource.Store.
func Store(t testing.TB, collection string) rest.Store {
	t.Helper()
	return mongodb.NewStore(Mongo(t).Collection(collection))
}

// Mongo returns an empty database for the test, dropped at cleanup.
func Mongo(t testing.TB) *mongo.Database {
	t.Helper()
	c := MongoClient(t)
	name := unsafeChars.ReplaceAllString(t.Name(), "_")
	if len(name) > 40 {
		name = name[len(name)-40:]
	}
	db := c.Database(fmt.Sprintf("t_%s_%d", name, time.Now().UnixNano()%1e6))
	t.Cleanup(func() { db.Drop(context.Background()) })
	return db
}

// DB adds assertions to a test database.
type DB struct {
	*mongo.Database
}

// HasIndex reports whether collection has an index whose first key is field.
func (db *DB) HasIndex(t testing.TB, collection, field string) bool {
	t.Helper()
	cur, err := db.Collection(collection).Indexes().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var indexes []struct {
		Key bson.D `bson:"key"`
	}
	if err := cur.All(context.Background(), &indexes); err != nil {
		t.Fatal(err)
	}
	for _, idx := range indexes {
		if len(idx.Key) > 0 && idx.Key[0].Key == field {
			return true
		}
	}
	return false
}

// Keys signs legacy auth-api tokens for tests.
type Keys struct {
	private *rsa.PrivateKey
	// PublicKeyFile is the PEM file to configure auth.Config.PublicKeyFile.
	PublicKeyFile string
}

// NewKeys generates a key pair.
func NewKeys(t testing.TB) *Keys {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	path := filepath.Join(t.TempDir(), "key_rsa.pub")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Keys{private: key, PublicKeyFile: path}
}

// Token signs a token for user valid for one hour.
func (k *Keys) Token(t testing.TB, user string) string {
	t.Helper()
	return k.TokenWith(t, map[string]any{"user": user})
}

// TokenWith signs a token with claims (plus exp), e.g. preferred_username
// as in a real Azure AD token.
func (k *Keys) TokenWith(t testing.TB, claims map[string]any) string {
	t.Helper()
	mc := jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}
	for key, v := range claims {
		mc[key] = v
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, mc)
	s, err := tok.SignedString(k.private)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Authenticator returns an enabled authenticator that accepts Token.
func (k *Keys) Authenticator(t testing.TB) *auth.Authenticator {
	t.Helper()
	a, err := auth.New(auth.Config{Enabled: true, PublicKeyFile: k.PublicKeyFile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// DisabledAuth returns an authenticator that accepts every request.
func DisabledAuth(t testing.TB) *auth.Authenticator {
	t.Helper()
	a, err := auth.New(auth.Config{Enabled: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Response is a decoded HTTP response.
type Response struct {
	Status int
	Header http.Header
	Raw    string
	// JSON is the decoded body: map[string]any, []any or nil.
	JSON any
}

// Map returns the body as an object.
func (r *Response) Map() map[string]any {
	m, _ := r.JSON.(map[string]any)
	return m
}

// String returns the top level field as a string.
func (r *Response) String(field string) string {
	s, _ := r.Map()[field].(string)
	return s
}

// Client calls a handler in process.
type Client struct {
	t       testing.TB
	handler http.Handler
	// Header is sent with every request (e.g. Authorization).
	Header http.Header
}

// NewClient builds a client for handler.
func NewClient(t testing.TB, handler http.Handler) *Client {
	return &Client{t: t, handler: handler, Header: http.Header{}}
}

// Anonymous returns a client for the same handler without the headers.
func (c *Client) Anonymous() *Client {
	return &Client{t: c.t, handler: c.handler, Header: http.Header{}}
}

// Do sends a request; body may be a string (sent as is) or a value encoded
// as JSON. headers are name/value pairs.
func (c *Client) Do(method, path string, body any, headers ...string) *Response {
	c.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range c.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)

	res := &Response{Status: rec.Code, Header: rec.Header(), Raw: rec.Body.String()}
	if rec.Body.Len() > 0 {
		json.Unmarshal(rec.Body.Bytes(), &res.JSON)
	}
	return res
}

// Service builds a whole service (app.Build) on an isolated database and
// returns a client for it.
func Service(t testing.TB, spec app.Spec, cfg config.Config, authn *auth.Authenticator) (*Client, *mongo.Database) {
	t.Helper()
	db := Mongo(t)
	if cfg == nil {
		cfg = config.Config{}
	}
	handler, err := app.Build(context.Background(), spec, app.Deps{
		Config: cfg,
		Mongo:  db.Client(),
		DB:     db,
		Auth:   authn,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(t, handler), db
}

// DryRun builds the service again on db, the database of Service, in dry
// run: the tests prepare the data with the real service and repeat the
// writes on this one. Background tasks registered with Env.Go and writes
// outside a transaction (mongodb.WriteGuard, as in app.Run) fail the test.
func DryRun(t testing.TB, spec app.Spec, cfg config.Config, authn *auth.Authenticator, db *mongo.Database) *Client {
	t.Helper()
	guarded, guard := GuardedMongo(t, db)
	t.Cleanup(func() {
		if n := guard.Violations(); n > 0 {
			t.Errorf("dry run: %d writes outside a transaction (see the log)", n)
		}
	})
	handler, err := app.Build(context.Background(), spec, app.Deps{
		Config: cfg,
		Mongo:  guarded.Client(),
		DB:     guarded,
		Auth:   authn,
		Start: func(name string, _ func(ctx context.Context) error) {
			t.Errorf("dry run started the background task %s", name)
		},
		DryRun:     true,
		WriteGuard: guard,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(t, handler)
}

// GuardedMongo opens db again on a client of its own with a
// mongodb.WriteGuard, as app.Run does in dry run.
func GuardedMongo(t testing.TB, db *mongo.Database) (*mongo.Database, *mongodb.WriteGuard) {
	t.Helper()
	guard := &mongodb.WriteGuard{}
	c, err := mongo.Connect(options.Client().ApplyURI(os.Getenv("MONGO_TEST_URI")).SetMonitor(guard.Monitor()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Disconnect(context.Background()) })
	return c.Database(db.Name()), guard
}

// ESCall is a request received by ESRecorder.
type ESCall struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

// ESRecorder is a fake Elasticsearch that records the writes.
type ESRecorder struct {
	*httptest.Server

	mu    sync.Mutex
	calls []ESCall
	// Fail makes every request answer 500.
	Fail bool
}

// NewES starts the recorder.
func NewES(t testing.TB) *ESRecorder {
	es := &ESRecorder{}
	es.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		es.mu.Lock()
		if r.Method != http.MethodHead {
			es.calls = append(es.calls, ESCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})
		}
		fail := es.Fail
		es.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(es.Close)
	return es
}

// Calls returns the recorded writes.
func (es *ESRecorder) Calls() []ESCall {
	es.mu.Lock()
	defer es.mu.Unlock()
	return append([]ESCall(nil), es.calls...)
}

// Reset forgets the recorded writes.
func (es *ESRecorder) Reset() {
	es.mu.Lock()
	es.calls = nil
	es.mu.Unlock()
}

// Hosts is the value for elasticsearch.hosts.
func (es *ESRecorder) Hosts() []any { return []any{es.URL} }
