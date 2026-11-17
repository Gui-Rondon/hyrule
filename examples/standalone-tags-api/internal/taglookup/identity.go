package taglookup

import (
	"context"
	"crypto/rsa"
	"errors"
	"math/big"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

const MaxToken = 8 * 1024

type tokenKey struct{}

// WithToken carries an untrusted compact JWT, without a Bearer prefix. It grants
// no identity: Verify and Authorize validate its signature and claims each time.
// Transports must extract credentials without logging the token.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}

// JWTConfig pins application trust. Keys are provisioned by the application,
// never discovered from token headers. There is deliberately no disabled mode.
type JWTConfig struct {
	Issuer, Audience, Scope string
	Keys                    map[string]*rsa.PublicKey
}

// JWTIdentity binds one issuer, audience and resource scope. Rebuild it with a
// new key set for rotation. It performs no discovery, network access or fallback
// to the legacy REST authenticator and does not register a transport endpoint.
type JWTIdentity struct {
	issuer, audience, scope string
	keys                    map[string]*rsa.PublicKey
}

type identityClaims struct {
	jwt.RegisteredClaims
	Scope string `json:"scope"`
}

func NewJWTIdentity(c JWTConfig) (*JWTIdentity, error) {
	for _, v := range []string{c.Issuer, c.Audience, c.Scope} {
		if v == "" || strings.TrimSpace(v) != v {
			return nil, errors.New("taglookup: explicit issuer, audience and scope are required")
		}
	}
	if len(c.Keys) == 0 {
		return nil, errors.New("taglookup: trusted RSA keys are required")
	}
	keys := make(map[string]*rsa.PublicKey, len(c.Keys))
	for kid, key := range c.Keys {
		if kid == "" || strings.TrimSpace(kid) != kid || len(kid) > 256 || key == nil || key.N == nil || key.N.Sign() <= 0 || key.N.BitLen() < 2048 || key.E < 3 || key.E%2 == 0 {
			return nil, errors.New("taglookup: invalid key ID or RSA public key")
		}
		// Copy the map and mutable modulus; caller changes must not alter trust.
		keys[kid] = &rsa.PublicKey{N: new(big.Int).Set(key.N), E: key.E}
	}
	return &JWTIdentity{issuer: c.Issuer, audience: c.Audience, scope: c.Scope, keys: keys}, nil
}

// Verify accepts only a signed, unexpired RS256 token with the pinned issuer,
// audience and a nonempty subject. It never trusts auth.WithClaims or a username.
func (i *JWTIdentity) Verify(ctx context.Context) (Principal, error) {
	claims, err := i.claims(ctx)
	if err != nil {
		return Principal{}, err
	}
	return Principal{Subject: claims.Subject}, nil
}

// Authorize revalidates the request token so an arbitrary Principal or claims
// context cannot manufacture permissions. Only the exact space-delimited scope
// tags:read authorizes this one tool version on the bound application resource.
func (i *JWTIdentity) Authorize(ctx context.Context, p Principal, a Action) error {
	claims, err := i.claims(ctx)
	if err != nil {
		return err
	}
	if p.Subject != claims.Subject || a != (Action{Name, Version, Permission, i.scope}) {
		return ErrForbidden
	}
	for _, scope := range strings.Split(claims.Scope, " ") {
		if scope == Permission {
			return nil
		}
	}
	return ErrForbidden
}

func (i *JWTIdentity) claims(ctx context.Context) (*identityClaims, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	raw, _ := ctx.Value(tokenKey{}).(string)
	if i == nil || len(raw) == 0 || len(raw) > MaxToken {
		return nil, ErrIdentity
	}
	claims := &identityClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" || len(kid) > 256 {
			return nil, ErrIdentity
		}
		key, ok := i.keys[kid]
		if !ok {
			return nil, ErrIdentity
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(i.issuer), jwt.WithAudience(i.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if ctxErr := contextError(ctx); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil || token == nil || !token.Valid || strings.TrimSpace(claims.Subject) == "" || len(claims.Subject) > 256 {
		return nil, ErrIdentity
	}
	return claims, nil
}

// AuthorizeKnowledge grants only the two read tools on the configured corpus.
// Document ACLs remain mandatory in the retrieval store, in addition to scope.
func (i *JWTIdentity) AuthorizeKnowledge(ctx context.Context, p Principal, a Action) error {
	claims, err := i.claims(ctx)
	if err != nil {
		return err
	}
	if p.Subject != claims.Subject || a.Version != "1" || a.Permission != "knowledge:read" || a.Scope != i.scope || (a.Tool != "knowledge.search" && a.Tool != "knowledge.fetch") {
		return ErrForbidden
	}
	for _, permission := range strings.Split(claims.Scope, " ") {
		if permission == "knowledge:read" {
			return nil
		}
	}
	return ErrForbidden
}
