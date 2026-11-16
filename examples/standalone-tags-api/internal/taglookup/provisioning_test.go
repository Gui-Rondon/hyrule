package taglookup

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publicKeyFile(t *testing.T, key *rsa.PublicKey) string {
	t.Helper()
	data, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "public.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: data}), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestProvisionedPublicKeyValidatesRealToken(t *testing.T) {
	key := rsaKey(t)
	file := publicKeyFile(t, &key.PublicKey)
	config := JWTFileConfig{Issuer: testIssuer, Audience: testAudience, Scope: testScope, KeyID: "key-1", PublicKeyFile: file}
	id, err := LoadJWTIdentity(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithToken(context.Background(), signed(t, key, "key-1", tokenClaims()))
	if _, err = id.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	// Key files are loaded once; replacing one does not silently change trust.
	if err = os.WriteFile(file, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = id.Verify(ctx); err != nil {
		t.Fatal("existing verifier changed after key replacement")
	}
	if _, err = LoadJWTIdentity(config); err == nil {
		t.Fatal("invalid replacement key accepted")
	}
	for _, change := range []func(*JWTFileConfig){func(c *JWTFileConfig) { c.PublicKeyFile = "missing" }, func(c *JWTFileConfig) { c.Issuer = "" }, func(c *JWTFileConfig) { c.Audience = "" }, func(c *JWTFileConfig) { c.Scope = "" }, func(c *JWTFileConfig) { c.KeyID = "" }} {
		c := config
		c.PublicKeyFile = publicKeyFile(t, &key.PublicKey)
		change(&c)
		if _, err = LoadJWTIdentity(c); err == nil {
			t.Fatal("missing provisioning configuration accepted")
		}
	}
}
func TestProvisioningRejectsPrivateAndOversizedKeys(t *testing.T) {
	key := rsaKey(t)
	path := filepath.Join(t.TempDir(), "key.pem")
	for _, data := range [][]byte{pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), []byte(strings.Repeat("x", 64*1024+1))} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadJWTIdentity(JWTFileConfig{Issuer: testIssuer, Audience: testAudience, Scope: testScope, KeyID: "key-1", PublicKeyFile: path}); err == nil {
			t.Fatal("invalid public key file accepted")
		}
	}
}
