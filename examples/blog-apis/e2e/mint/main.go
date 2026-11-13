// Command mint signs an RS256 JWT with the e2e harness's generated
// legacy key, for exercising platform/auth's legacy fallback from JMeter
// (which has no RS256 JWT signer out of the box).
//
//	go run ./examples/blog-apis/e2e/mint <private-key-path> <username> [ttl]
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: mint <private-key-path> <username> [ttl]")
		os.Exit(1)
	}
	keyPath, username := os.Args[1], os.Args[2]
	ttl := 24 * time.Hour
	if len(os.Args) > 3 {
		d, err := time.ParseDuration(os.Args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad ttl:", err)
			os.Exit(1)
		}
		ttl = d
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM(keyData)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	claims := jwt.MapClaims{
		"user": username,
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(signed)
}
