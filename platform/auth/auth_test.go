package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/ssh"
)

func TestTokenFromRequest(t *testing.T) {
	basic := base64.StdEncoding.EncodeToString([]byte("tok-user:secret"))
	cases := []struct {
		name   string
		header string
		cookie string
		want   string
	}{
		{"basic username", "Basic " + basic, "", "tok-user"},
		{"token prefix", "Token abc.def.ghi", "", "abc.def.ghi"},
		{"bare Basic: prefix", "Basic: abc.def.ghi", "", "abc.def.ghi"},
		{"raw header", "abc.def.ghi", "", "abc.def.ghi"},
		// get_token_from_request: without a username, b64decode, ascii and
		// split(':')[1]; any exception keeps the value after "Basic "
		{"basic password", "Basic " + base64.StdEncoding.EncodeToString([]byte(":tok-pass")), "", "tok-pass"},
		{"basic second segment", "Basic " + base64.StdEncoding.EncodeToString([]byte(":a:b")), "", "a"},
		{"basic without colon", "Basic " + base64.StdEncoding.EncodeToString([]byte("semdoispontos")), "", base64.StdEncoding.EncodeToString([]byte("semdoispontos"))},
		{"basic not ascii", "Basic " + base64.StdEncoding.EncodeToString([]byte(":çã")), "", base64.StdEncoding.EncodeToString([]byte(":çã"))},
		{"token lowercase", "token abc", "", "abc"},
		{"token without value", "Token", "", ""},
		{"cookie", "", "from-cookie", "from-cookie"},
		{"nothing", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "token", Value: tc.cookie})
			}
			if got := TokenFromRequest(r, "token"); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthenticateLegacyToken(t *testing.T) {
	key, keyFile := newKey(t)
	a, err := New(Config{Enabled: true, PublicKeyFile: keyFile}, nil)
	if err != nil {
		t.Fatal(err)
	}

	token := sign(t, key, "", jwt.MapClaims{"user": "fulano", "exp": time.Now().Add(time.Minute).Unix()})
	claims, err := a.Authenticate(request("Token " + token))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Username() != "fulano" {
		t.Errorf("username = %q", claims.Username())
	}

	expired := sign(t, key, "", jwt.MapClaims{"user": "fulano", "exp": time.Now().Add(-time.Minute).Unix()})
	if _, err := a.Authenticate(request("Token " + expired)); err == nil {
		t.Error("expired token must be rejected")
	}
	if _, err := a.Authenticate(request("")); err == nil {
		t.Error("missing token must be rejected")
	}
}

func TestAuthenticateLegacyTokenWithOpenSSHKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := ssh.NewPublicKey(&key.PublicKey)
	path := filepath.Join(t.TempDir(), "key_rsa.pub")
	os.WriteFile(path, ssh.MarshalAuthorizedKey(pub), 0o600)

	a, err := New(Config{Enabled: true, PublicKeyFile: path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := sign(t, key, "", jwt.MapClaims{"user": "fulano", "exp": time.Now().Add(time.Minute).Unix()})
	if _, err := a.Authenticate(request(token)); err != nil {
		t.Fatal(err)
	}
}

// Without PublicKeyFile, New succeeds and the legacy fallback simply never
// matches — no key ships embedded in the binary.
func TestNoPublicKeyFileDisablesLegacyFallback(t *testing.T) {
	a, err := New(Config{Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := newKey(t)
	exp := time.Now().Add(time.Minute).Unix()
	legacy := sign(t, key, "", jwt.MapClaims{"user": "someone", "exp": exp})
	if _, err := a.Authenticate(request(legacy)); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticateDisabledAcceptsAnything(t *testing.T) {
	a, _ := New(Config{Enabled: false}, nil)
	if _, err := a.Authenticate(request("")); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticateAzureThenLegacy(t *testing.T) {
	azureKey, _ := newKey(t)
	legacyKey, legacyFile := newKey(t)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tenant/v2.0/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"jwks_uri": srv.URL + "/keys"})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
				"kid": "k1", "kty": "RSA",
				"n": base64.RawURLEncoding.EncodeToString(azureKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(azureKey.E)).Bytes()),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	azure := NewAzureVerifier("tenant", "https://issuer", []string{"app-dev", "app-prd"})
	azure.authority = srv.URL
	a, err := New(Config{Enabled: true, PublicKeyFile: legacyFile}, azure)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Minute).Unix()

	good := sign(t, azureKey, "k1", jwt.MapClaims{"iss": "https://issuer", "aud": "app-prd", "preferred_username": "x@example.com", "exp": exp})
	claims, err := a.Authenticate(request(good))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Username() != "x@example.com" {
		t.Errorf("username = %q", claims.Username())
	}

	wrongAud := sign(t, azureKey, "k1", jwt.MapClaims{"iss": "https://issuer", "aud": "other", "exp": exp})
	if _, err := a.Authenticate(request(wrongAud)); err == nil {
		t.Error("azure token with wrong audience must be rejected without legacy fallback")
	}

	legacy := sign(t, legacyKey, "", jwt.MapClaims{"user": "legado", "exp": exp})
	claims, err = a.Authenticate(request(legacy))
	if err != nil {
		t.Fatalf("token without kid must fall back to legacy: %v", err)
	}
	if claims.Username() != "legado" {
		t.Errorf("username = %q", claims.Username())
	}
}

func request(authorization string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	return r
}

func newKey(t *testing.T) (*rsa.PrivateKey, string) {
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
	return key, path
}

func sign(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The Azure keys are fetched once and cached; an unknown kid refreshes them
// once (rotation); keys that are not RSA or are malformed are skipped; a
// failed discovery or JWKS is an error, not a fallback to legacy.
func TestAzureKeys(t *testing.T) {
	k1, _ := newKey(t)
	k2, _ := newKey(t)
	jwk := func(kid string, key *rsa.PrivateKey) map[string]string {
		return map[string]string{"kid": kid, "kty": "RSA",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
	}
	var fetches int
	current := []map[string]string{jwk("k1", k1), {"kid": "ec", "kty": "EC"}, {"kid": "bad", "kty": "RSA", "n": "!!", "e": "AQAB"}}
	status := http.StatusOK
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tenant/v2.0/.well-known/openid-configuration":
			fetches++
			json.NewEncoder(w).Encode(map[string]string{"jwks_uri": srv.URL + "/keys"})
		case "/keys":
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"keys": current})
		}
	}))
	defer srv.Close()
	v := NewAzureVerifier("tenant", "", nil)
	v.authority = srv.URL
	ctx := context.Background()
	exp := time.Now().Add(time.Minute).Unix()

	tok1 := sign(t, k1, "k1", jwt.MapClaims{"exp": exp})
	for range 2 {
		if _, err := v.Verify(ctx, tok1); err != nil {
			t.Fatal(err)
		}
	}
	if fetches != 1 {
		t.Errorf("fetches = %d, want 1 (cached)", fetches)
	}
	if _, err := v.Verify(ctx, sign(t, k1, "k9", jwt.MapClaims{"exp": exp})); !errors.Is(err, ErrNotAzureToken) || fetches != 2 {
		t.Errorf("unknown kid = %v, fetches %d", err, fetches)
	}
	for _, kid := range []string{"ec", "bad"} {
		if _, err := v.Verify(ctx, sign(t, k1, kid, jwt.MapClaims{"exp": exp})); !errors.Is(err, ErrNotAzureToken) {
			t.Errorf("skipped key %s = %v", kid, err)
		}
	}
	current = []map[string]string{jwk("k2", k2)}
	if _, err := v.Verify(ctx, sign(t, k2, "k2", jwt.MapClaims{"exp": exp})); err != nil {
		t.Errorf("rotated key = %v", err)
	}
	if _, err := v.Verify(ctx, "nao-e-jwt"); !errors.Is(err, ErrNotAzureToken) {
		t.Errorf("garbage = %v", err)
	}
	status = http.StatusServiceUnavailable
	if _, err := v.Verify(ctx, sign(t, k2, "k3", jwt.MapClaims{"exp": exp})); err == nil || errors.Is(err, ErrNotAzureToken) {
		t.Errorf("JWKS 503 = %v", err)
	}
	v.authority = "http://127.0.0.1:1"
	if _, err := v.Verify(ctx, sign(t, k2, "k4", jwt.MapClaims{"exp": exp})); err == nil || errors.Is(err, ErrNotAzureToken) {
		t.Errorf("discovery down = %v", err)
	}

	// an audience is required when configured
	v = NewAzureVerifier("tenant", "", []string{"app"})
	v.authority = srv.URL
	status = http.StatusOK
	if _, err := v.Verify(ctx, sign(t, k2, "k2", jwt.MapClaims{"exp": exp})); !errors.Is(err, jwt.ErrTokenInvalidAudience) {
		t.Errorf("without aud = %v", err)
	}
}

// The public key: PEM or OpenSSH ssh-rsa; anything else fails at startup.
func TestPublicKeyFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o600)
		return p
	}
	for name, path := range map[string]string{
		"missing":     filepath.Join(dir, "nao-existe"),
		"not a key":   write("lixo", "nao e chave"),
		"bad ssh-rsa": write("ssh", "ssh-rsa AAAAinvalido x@y"),
		"ed25519":     write("ed", string(ssh.MarshalAuthorizedKey(edKey(t)))),
	} {
		if _, err := New(Config{Enabled: true, PublicKeyFile: path}, nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	a, _ := New(Config{Enabled: false}, nil)
	if _, err := a.VerifyLegacy("qualquer"); err != nil {
		t.Errorf("disabled VerifyLegacy = %v", err)
	}
}

func edKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
