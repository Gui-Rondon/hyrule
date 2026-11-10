package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrNotAzureToken means the token has no kid or a kid unknown to Azure AD,
// so the legacy validation must be tried (InvalidAuthorizationToken in
// eve_azure_ad_auth).
var ErrNotAzureToken = errors.New("not an azure ad token")

// AzureVerifier validates Azure AD v2.0 tokens, like
// eve_azure_ad_auth.verifier.AzureVerifier, caching the signing keys.
type AzureVerifier struct {
	authority string
	tenant    string
	issuer    string
	audiences []string
	client    *http.Client
	ttl       time.Duration

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewAzureVerifier builds a verifier. An empty tenant means "common".
func NewAzureVerifier(tenant, issuer string, audiences []string) *AzureVerifier {
	if tenant == "" {
		tenant = "common"
	}
	return &AzureVerifier{
		authority: "https://login.microsoftonline.com",
		tenant:    tenant,
		issuer:    issuer,
		audiences: audiences,
		client:    &http.Client{Timeout: 5 * time.Second},
		ttl:       time.Hour,
	}
}

// Verify validates signature, issuer and audience.
func (v *AzureVerifier) Verify(ctx context.Context, token string) (Claims, error) {
	parsed, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return nil, ErrNotAzureToken
	}
	kid, _ := parsed.Header["kid"].(string)
	if kid == "" {
		return nil, ErrNotAzureToken
	}
	key, err := v.key(ctx, kid)
	if err != nil {
		return nil, err
	}

	opts := []jwt.ParserOption{jwt.WithValidMethods([]string{"RS256"})}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return key, nil }, opts...); err != nil {
		return nil, err
	}
	if len(v.audiences) > 0 && !audienceMatches(claims, v.audiences) {
		return nil, jwt.ErrTokenInvalidAudience
	}
	return Claims(claims), nil
}

func audienceMatches(claims jwt.MapClaims, allowed []string) bool {
	auds, err := claims.GetAudience()
	if err != nil {
		return false
	}
	for _, a := range auds {
		for _, b := range allowed {
			if a == b {
				return true
			}
		}
	}
	return false
}

func (v *AzureVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if key, ok := v.keys[kid]; ok && time.Since(v.fetchedAt) < v.ttl {
		return key, nil
	}
	// unknown kid or expired cache: refresh once, keys may have rotated
	keys, err := v.fetchKeys(ctx)
	if err != nil {
		return nil, err
	}
	v.keys, v.fetchedAt = keys, time.Now()
	if key, ok := keys[kid]; ok {
		return key, nil
	}
	return nil, ErrNotAzureToken
}

func (v *AzureVerifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	var discovery struct {
		JwksURI string `json:"jwks_uri"`
	}
	url := fmt.Sprintf("%s/%s/v2.0/.well-known/openid-configuration", v.authority, v.tenant)
	if err := v.getJSON(ctx, url, &discovery); err != nil {
		return nil, err
	}
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := v.getJSON(ctx, discovery.JwksURI, &jwks); err != nil {
		return nil, err
	}
	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return keys, nil
}

func (v *AzureVerifier) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth: get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: get %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
