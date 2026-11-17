package taglookup

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/platform/auth"
	"github.com/golang-jwt/jwt/v5"
)

const testIssuer = "https://issuer.example.test"
const testAudience = "hyrule-tags-pilot"
const testScope = "local:tags"

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func tokenClaims() jwt.MapClaims {
	return jwt.MapClaims{"iss": testIssuer, "aud": testAudience, "sub": "reader", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Add(-time.Minute).Unix(), "scope": Permission}
}
func signed(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func identity(t *testing.T, key *rsa.PrivateKey) *JWTIdentity {
	t.Helper()
	i, err := NewJWTIdentity(JWTConfig{Issuer: testIssuer, Audience: testAudience, Scope: testScope, Keys: map[string]*rsa.PublicKey{"key-1": &key.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestJWTPilotAuthenticatesBeforeStorage(t *testing.T) {
	key := rsaKey(t)
	other := rsaKey(t)
	id := identity(t, key)
	for _, tc := range []struct {
		name   string
		change func(jwt.MapClaims)
		key    *rsa.PrivateKey
		kid    string
		want   error
	}{
		{name: "valid"},
		{name: "wrong issuer", change: func(c jwt.MapClaims) { c["iss"] = "https://other.example.test" }, want: ErrIdentity},
		{name: "missing issuer", change: func(c jwt.MapClaims) { delete(c, "iss") }, want: ErrIdentity},
		{name: "wrong audience", change: func(c jwt.MapClaims) { c["aud"] = "other-api" }, want: ErrIdentity},
		{name: "missing audience", change: func(c jwt.MapClaims) { delete(c, "aud") }, want: ErrIdentity},
		{name: "audience array", change: func(c jwt.MapClaims) { c["aud"] = []string{"other", testAudience} }},
		{name: "expired", change: func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }, want: ErrIdentity},
		{name: "missing expiration", change: func(c jwt.MapClaims) { delete(c, "exp") }, want: ErrIdentity},
		{name: "future nbf", change: func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Hour).Unix() }, want: ErrIdentity},
		{name: "future iat", change: func(c jwt.MapClaims) { c["iat"] = time.Now().Add(time.Hour).Unix() }, want: ErrIdentity},
		{name: "missing subject", change: func(c jwt.MapClaims) { delete(c, "sub") }, want: ErrIdentity},
		{name: "empty subject", change: func(c jwt.MapClaims) { c["sub"] = "  " }, want: ErrIdentity},
		{name: "long subject", change: func(c jwt.MapClaims) { c["sub"] = strings.Repeat("a", 257) }, want: ErrIdentity},
		{name: "wrong signature", key: other, want: ErrIdentity},
		{name: "unknown key", kid: "unknown", want: ErrIdentity},
		{name: "missing permission", change: func(c jwt.MapClaims) { delete(c, "scope") }, want: ErrForbidden},
		{name: "permission substring", change: func(c jwt.MapClaims) { c["scope"] = "other:tags:read tags:read:all" }, want: ErrForbidden},
		{name: "tab is not a scope separator", change: func(c jwt.MapClaims) { c["scope"] = "tags:read\tother:read" }, want: ErrForbidden},
		{name: "wildcard", change: func(c jwt.MapClaims) { c["scope"] = "tags:*" }, want: ErrForbidden},
		{name: "multiple scopes", change: func(c jwt.MapClaims) { c["scope"] = "other:read tags:read other:write" }},
		{name: "roles do not grant scope", change: func(c jwt.MapClaims) { delete(c, "scope"); c["roles"] = []string{Permission} }, want: ErrForbidden},
		{name: "invalid scope type", change: func(c jwt.MapClaims) { c["scope"] = []string{Permission} }, want: ErrIdentity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := tokenClaims()
			if tc.change != nil {
				tc.change(claims)
			}
			signingKey := key
			if tc.key != nil {
				signingKey = tc.key
			}
			kid := "key-1"
			if tc.kid != "" {
				kid = tc.kid
			}
			raw := signed(t, signingKey, kid, claims)
			r := setup(t, nil)
			r.d.Verify = id.Verify
			r.d.Authorize = id.Authorize
			r.rebuild(t)
			ctx := WithToken(context.Background(), raw)
			tool, describeErr := r.e.Describe(ctx)
			out, executeErr := r.e.Execute(ctx, strings.NewReader(`{"slug":"go"}`))
			if tc.want != nil {
				if !errors.Is(describeErr, tc.want) || !errors.Is(executeErr, tc.want) || tool != (Tool{}) || out != nil || r.a.reads != 0 {
					t.Fatalf("identity/policy bypass: %+v %s %v %v reads=%d", tool, out, describeErr, executeErr, r.a.reads)
				}
			} else if describeErr != nil || executeErr != nil || tool.Name != Name || !strings.Contains(string(out), `"slug":"go"`) || r.a.reads != 1 {
				t.Fatalf("valid token rejected: %+v %s %v %v", tool, out, describeErr, executeErr)
			}
			if len(r.records) != 2 {
				t.Fatal("missing auth outcome audit")
			}
			for _, rec := range r.records {
				if strings.Contains(rec.Outcome, raw) || strings.Contains(rec.Subject, raw) {
					t.Fatal("token leaked to audit")
				}
			}
		})
	}
}
func TestJWTRejectsUntrustedContextAndAlgorithms(t *testing.T) {
	key := rsaKey(t)
	id := identity(t, key)
	r := setup(t, nil)
	r.d.Verify = id.Verify
	r.d.Authorize = id.Authorize
	r.rebuild(t)
	// These are merely application context values, not a verified identity.
	forged := auth.WithClaims(context.Background(), auth.Claims{"sub": "reader", "scope": Permission})
	_, err := r.e.Execute(forged, strings.NewReader(`{"slug":"go"}`))
	if err != ErrIdentity {
		t.Fatalf("context claims trusted: %v", err)
	}
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenClaims())
	hs.Header["kid"] = "key-1"
	hsRaw, err := hs.SignedString([]byte("attacker secret"))
	if err != nil {
		t.Fatal(err)
	}
	rs := jwt.NewWithClaims(jwt.SigningMethodRS512, tokenClaims())
	rs.Header["kid"] = "key-1"
	rsRaw, err := rs.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	none := jwt.NewWithClaims(jwt.SigningMethodNone, tokenClaims())
	none.Header["kid"] = "key-1"
	noneRaw, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "bad-token", strings.Repeat("x", MaxToken+1), signed(t, key, "", tokenClaims()), hsRaw, rsRaw, noneRaw} {
		out, err := r.e.Execute(WithToken(forged, raw), strings.NewReader(`{"slug":"go"}`))
		if err != ErrIdentity || out != nil {
			t.Fatalf("untrusted token accepted: %v", err)
		}
	}
	if r.a.reads != 0 {
		t.Fatal("untrusted credentials reached storage")
	}
	claims := tokenClaims()
	claims["scope"] = "other:read"
	_, err = r.e.Execute(WithToken(forged, signed(t, key, "key-1", claims)), strings.NewReader(`{"slug":"go"}`))
	if err != ErrForbidden {
		t.Fatalf("context claims granted permission: %v", err)
	}
}
func TestJWTPolicyBindsPrincipalToolVersionAndResource(t *testing.T) {
	key := rsaKey(t)
	id := identity(t, key)
	ctx := WithToken(context.Background(), signed(t, key, "key-1", tokenClaims()))
	p, err := id.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	action := Action{Name, Version, Permission, testScope}
	if err := id.Authorize(ctx, p, action); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Action){func(a *Action) { a.Tool = "other" }, func(a *Action) { a.Version = "2" }, func(a *Action) { a.Permission = "tags:write" }, func(a *Action) { a.Scope = "other:tags" }} {
		a := action
		change(&a)
		if err := id.Authorize(ctx, p, a); err != ErrForbidden {
			t.Fatalf("action scope bypass: %v", err)
		}
	}
	if err := id.Authorize(ctx, Principal{Subject: "other"}, action); err != ErrForbidden {
		t.Fatalf("principal substitution: %v", err)
	}
	if err := id.Authorize(context.Background(), p, action); err != ErrIdentity {
		t.Fatalf("principal reused without credentials: %v", err)
	}
	r := setup(t, nil)
	r.d.Verify = id.Verify
	r.d.Authorize = id.Authorize
	r.d.Scope = "other:tags"
	r.rebuild(t)
	_, err = r.e.Execute(ctx, strings.NewReader(`{"slug":"go"}`))
	if err != ErrForbidden || r.a.reads != 0 {
		t.Fatalf("resource binding bypass: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := id.Verify(canceled); err != ErrCanceled {
		t.Fatalf("cancellation lost: %v", err)
	}
}
func TestJWTTrustConfigurationAndKeyCopy(t *testing.T) {
	key := rsaKey(t)
	config := JWTConfig{Issuer: testIssuer, Audience: testAudience, Scope: testScope, Keys: map[string]*rsa.PublicKey{"key-1": &key.PublicKey}}
	for _, change := range []func(*JWTConfig){func(c *JWTConfig) { c.Issuer = "" }, func(c *JWTConfig) { c.Audience = "" }, func(c *JWTConfig) { c.Scope = "" }, func(c *JWTConfig) { c.Keys = nil }, func(c *JWTConfig) { c.Keys = map[string]*rsa.PublicKey{"": &key.PublicKey} }, func(c *JWTConfig) { c.Keys = map[string]*rsa.PublicKey{"key-1": nil} }, func(c *JWTConfig) { c.Issuer = " " + testIssuer }} {
		c := config
		change(&c)
		if _, err := NewJWTIdentity(c); err == nil {
			t.Fatal("invalid trust configuration accepted")
		}
	}
	weak, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	c := config
	c.Keys = map[string]*rsa.PublicKey{"weak": &weak.PublicKey}
	if _, err := NewJWTIdentity(c); err == nil {
		t.Fatal("weak key accepted")
	}
	raw := signed(t, key, "key-1", tokenClaims())
	id, err := NewJWTIdentity(config)
	if err != nil {
		t.Fatal(err)
	}
	delete(config.Keys, "key-1")
	key.PublicKey.N.SetInt64(1)
	if _, err := id.Verify(WithToken(context.Background(), raw)); err != nil {
		t.Fatalf("caller changed copied trust: %v", err)
	}
}

func TestJWTAuditReceivesNoCredentialContext(t *testing.T) {
	key := rsaKey(t)
	id := identity(t, key)
	r := setup(t, nil)
	r.d.Verify = id.Verify
	r.d.Authorize = id.Authorize
	audited := false
	r.d.Audit = func(ctx context.Context, rec Record) error {
		audited = true
		if ctx.Value(tokenKey{}) != nil || len(auth.FromContext(ctx)) != 0 {
			t.Error("audit inherited credentials/claims")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > auditTimeout {
			t.Error("audit has no independent budget")
		}
		if rec.Subject != "reader" || rec.Scope != testScope || !rec.Authorized {
			t.Error("missing verified audit metadata")
		}
		return nil
	}
	r.rebuild(t)
	ctx := auth.WithClaims(context.Background(), auth.Claims{"untrusted": "do not forward"})
	ctx = WithToken(ctx, signed(t, key, "key-1", tokenClaims()))
	if _, err := r.e.Execute(ctx, strings.NewReader(`{"slug":"go"}`)); err != nil {
		t.Fatal(err)
	}
	if !audited {
		t.Fatal("missing audit")
	}
}
