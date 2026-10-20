// Command adduser creates a portal user, typically the first admin.
//
//	DATABASE_URL=... go run ./cmd/adduser -email you@example.com -role admin -name "Your Name"
//
// The password is read from the terminal without echo. For scripts, pass
// -password-stdin and pipe the password in (never put it on the command line).
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"github.com/rpsoftech/cad-portal/backend/internal/migrate"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func main() {
	email := flag.String("email", "", "user email (required)")
	name := flag.String("name", "", "display name")
	role := flag.String("role", "admin", "admin, designer or production")
	stdin := flag.Bool("password-stdin", false, "read the password from standard input")
	flag.Parse()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" || *email == "" {
		flag.Usage()
		log.Fatal("DATABASE_URL and -email are required")
	}
	pw, err := readPassword(*stdin)
	if err != nil {
		log.Fatal(err)
	}

	// Make sure the users table exists even on a fresh database.
	if err := migrate.Up(dbURL); err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	u, err := service.New(pool).CreateUser(ctx, service.CreateUserInput{
		Email: *email, Name: *name, Password: pw, Role: *role,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("created %s (%s) with id %d\n", u.Email, u.Role, u.ID)
}

func readPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no password on standard input")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	if !term.IsTerminal(int(syscall.Stdin)) {
		return "", errors.New("standard input is not a terminal; use -password-stdin")
	}
	fmt.Fprint(os.Stderr, "Password: ")
	a, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Repeat password: ")
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(a) != string(b) {
		return "", errors.New("passwords do not match")
	}
	return string(a), nil
}
