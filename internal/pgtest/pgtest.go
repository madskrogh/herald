// Package pgtest runs one Postgres container per test package and gives each
// test a database of its own, so integration tests can run in parallel
// against real Postgres.
package pgtest

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// image matches the Postgres version in compose.yaml.
const image = "postgres:18-alpine"

// serverURL connects to the container's maintenance database. It is empty
// until Main has started the container.
var serverURL string

// Main starts a Postgres container, runs the package's tests and removes the
// container. Call it from TestMain. With -short it runs the tests without a
// container, and integration tests skip themselves.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return m.Run()
	}

	ctx := context.Background()
	ctr, err := postgres.Run(ctx, image,
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("herald"),
		postgres.WithPassword("herald"),
		postgres.BasicWaitStrategies(),
	)
	defer func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			fmt.Fprintln(os.Stderr, "pgtest: terminate container:", err)
		}
	}()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgtest: start postgres:", err)
		return 1
	}
	serverURL, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgtest: connection string:", err)
		return 1
	}
	return m.Run()
}

// NewDatabase creates an empty database for t, drops it when t finishes, and
// returns its connection URL.
func NewDatabase(t *testing.T) string {
	t.Helper()
	if serverURL == "" {
		t.Fatal("pgtest: no Postgres container; call pgtest.Main from TestMain")
	}

	name := "test_" + rand.Text()
	// Identifiers can't be query parameters, so the name is quoted into the
	// statement. It is generated above, never taken from input.
	ident := pgx.Identifier{name}.Sanitize()
	exec(t, "CREATE DATABASE "+ident)
	t.Cleanup(func() { exec(t, "DROP DATABASE "+ident+" WITH (FORCE)") })

	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("pgtest: parse server URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// exec runs sql on the maintenance database. It uses a context of its own
// because it also runs in cleanup, after the test's context is cancelled.
func exec(t *testing.T, sql string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("pgtest: %s: %v", sql, err)
	}
}
