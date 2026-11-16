package taglookup

import (
	"crypto/rsa"
	"errors"
	"io"
	"os"

	"github.com/golang-jwt/jwt/v5"
)

// JWTFileConfig provisions one trusted public key. Issuer, audience, scope and
// key ID remain explicit application configuration, never token-derived values.
// Use NewJWTIdentity directly to provision overlapping keys during rotation.
type JWTFileConfig struct {
	Issuer, Audience, Scope, KeyID, PublicKeyFile string
}

func LoadJWTIdentity(c JWTFileConfig) (*JWTIdentity, error) {
	f, err := os.Open(c.PublicKeyFile)
	if err != nil {
		return nil, errors.New("taglookup: cannot read trusted public key")
	}
	defer f.Close()
	const maxPEM = 64 * 1024
	data, err := io.ReadAll(io.LimitReader(f, maxPEM+1))
	if err != nil || len(data) > maxPEM {
		return nil, errors.New("taglookup: invalid trusted public key file")
	}
	key, err := jwt.ParseRSAPublicKeyFromPEM(data)
	if err != nil {
		return nil, errors.New("taglookup: expected an RSA public key PEM")
	}
	return NewJWTIdentity(JWTConfig{Issuer: c.Issuer, Audience: c.Audience, Scope: c.Scope, Keys: map[string]*rsa.PublicKey{c.KeyID: key}})
}
