// Package auth validates requests: an Azure AD token is tried first and,
// when the token isn't an Azure one (no kid, unknown kid), it falls back to
// a static RS256 key — for a secondary issuer, service-to-service tokens,
// or any other static key a deployment wants to trust alongside Azure AD.
package auth

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/ssh"
)

// Claims is the decoded token payload.
type Claims map[string]any

// Username returns the preferred_username or user claim, in that order.
func (c Claims) Username() string {
	if v, ok := c["preferred_username"].(string); ok && v != "" {
		return v
	}
	if v, ok := c["user"].(string); ok {
		return v
	}
	return ""
}

// Config holds the authentication settings.
type Config struct {
	Enabled bool
	// CookieName is read when no Authorization header carries a token
	// (default "token").
	CookieName string
	// PublicKeyFile is the static RS256 key for the legacy fallback (PEM or
	// "ssh-rsa ..."). Without it, that fallback is disabled: a non-Azure
	// token is simply rejected.
	PublicKeyFile string
	Tenant        string
	Issuer        string
	Audiences     []string
}

// Authenticator validates requests.
type Authenticator struct {
	cfg       Config
	legacyKey *rsa.PublicKey
	azure     *AzureVerifier
}

// New builds an Authenticator. A PublicKeyFile that fails to read or parse
// fails New, so a typo in the config never silently disables the fallback.
func New(cfg Config, azure *AzureVerifier) (*Authenticator, error) {
	a := &Authenticator{cfg: cfg, azure: azure}
	if cfg.CookieName == "" {
		a.cfg.CookieName = "token"
	}
	if cfg.PublicKeyFile != "" {
		keyData, err := os.ReadFile(cfg.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("auth: read public key: %w", err)
		}
		key, err := parseRSAPublicKey(keyData)
		if err != nil {
			return nil, fmt.Errorf("auth: parse public key: %w", err)
		}
		a.legacyKey = key
	}
	return a, nil
}

// parseRSAPublicKey accepts PEM and OpenSSH ("ssh-rsa ...") keys, like PyJWT.
func parseRSAPublicKey(data []byte) (*rsa.PublicKey, error) {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("ssh-rsa")) {
		pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			return nil, err
		}
		cpk, ok := pub.(ssh.CryptoPublicKey)
		if !ok {
			return nil, errors.New("unsupported ssh key")
		}
		rsaKey, ok := cpk.CryptoPublicKey().(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("ssh key is not RSA")
		}
		return rsaKey, nil
	}
	return jwt.ParseRSAPublicKeyFromPEM(data)
}

// Enabled reports whether authentication is enforced.
func (a *Authenticator) Enabled() bool { return a.cfg.Enabled }

// ErrUnauthorized is returned for missing or invalid credentials.
var ErrUnauthorized = errors.New("please provide proper credentials")

// Authenticate validates the request token. With auth disabled it returns
// empty claims and no error.
func (a *Authenticator) Authenticate(r *http.Request) (Claims, error) {
	if !a.cfg.Enabled {
		return Claims{}, nil
	}
	token := TokenFromRequest(r, a.cfg.CookieName)
	if token == "" {
		return nil, ErrUnauthorized
	}
	if a.azure != nil {
		claims, err := a.azure.Verify(r.Context(), token)
		if err == nil {
			return claims, nil
		}
		// The legacy fallback only applies when the token isn't an Azure
		// one; a bad Azure token (expired, wrong audience) is rejected.
		if !errors.Is(err, ErrNotAzureToken) {
			return nil, ErrUnauthorized
		}
	}
	if claims, err := a.verifyLegacy(token); err == nil {
		return claims, nil
	}
	return nil, ErrUnauthorized
}

// VerifyLegacy validates a token against the static RS256 key
// (Config.PublicKeyFile); with authentication disabled every token is
// valid.
func (a *Authenticator) VerifyLegacy(token string) (Claims, error) {
	if !a.cfg.Enabled {
		return Claims{}, nil
	}
	return a.verifyLegacy(token)
}

// Azure returns the Azure verifier, or nil when Azure is not configured.
func (a *Authenticator) Azure() *AzureVerifier { return a.azure }

func (a *Authenticator) verifyLegacy(token string) (Claims, error) {
	if a.legacyKey == nil {
		return nil, ErrUnauthorized
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return a.legacyKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}))
	if err != nil {
		return nil, err
	}
	return Claims(claims), nil
}

// TokenFromRequest extracts the token from the request, in this order:
//
//	Authorization: Basic base64(<token>:<anything>)  -> the username
//	Authorization: Token <token>
//	Authorization: Basic: <token>                     -> a bare "Basic:" prefix
//	Authorization: <token>
//	Cookie: <cookieName>=<token>
func TokenFromRequest(r *http.Request, cookieName string) string {
	if user, _, ok := r.BasicAuth(); ok && user != "" {
		return user
	}
	if header := strings.TrimSpace(r.Header.Get("Authorization")); header != "" {
		lower := strings.ToLower(header)
		switch {
		case strings.HasPrefix(lower, "token"):
			return field(header, 1)
		case strings.HasPrefix(lower, "basic"):
			value := field(header, 1)
			// a real basic credential carries the token as the password part
			if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && isASCII(decoded) {
				// split(':')[1]: the segment after the first colon
				if _, rest, ok := strings.Cut(string(decoded), ":"); ok {
					pass, _, _ := strings.Cut(rest, ":")
					return pass
				}
			}
			return value
		default:
			return header
		}
	}
	if c, err := r.Cookie(cookieName); err == nil {
		return c.Value
	}
	return ""
}

func field(s string, i int) string {
	parts := strings.Split(s, " ")
	if i < len(parts) {
		return parts[i]
	}
	return ""
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c > 127 {
			return false
		}
	}
	return true
}

type claimsKey struct{}

// WithClaims stores the claims in the context.
func WithClaims(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

// FromContext returns the claims of an authenticated request.
func FromContext(ctx context.Context) Claims {
	c, _ := ctx.Value(claimsKey{}).(Claims)
	return c
}
