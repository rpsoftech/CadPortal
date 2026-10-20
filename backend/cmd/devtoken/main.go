// Command devtoken prints a short-lived bearer token for local development.
// Usage: JWT_SECRET=... go run ./cmd/devtoken <admin|designer|production> [subject]
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
)

func main() {
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 || len(os.Args) < 2 {
		log.Fatal("usage: JWT_SECRET=<32+ chars> devtoken <admin|designer|production> [subject]")
	}
	sub := "dev"
	if len(os.Args) > 2 {
		sub = os.Args[2]
	}
	tok, err := auth.Issue([]byte(secret), sub, auth.Role(os.Args[1]), 8*time.Hour)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(tok)
}
