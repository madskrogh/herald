// Command herald-admin is the operator CLI for Herald. It manages Tenants and
// their API keys by connecting directly to the ingest schema.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/madskrogh/herald/internal/ingest"
)

const usage = `usage: herald-admin <command> [arguments]

herald-admin is the operator CLI for Herald. It connects directly to the
database in HERALD_DATABASE_URL.

Commands:
  tenant create --name <name>    Create a Tenant and print its ID
  apikey create --tenant <id>    Create an API key for a Tenant and print it
  help                           Show this help
`

// action carries out a command whose arguments have been parsed.
type action func(ctx context.Context, store *ingest.Store, stdout, stderr io.Writer) error

// commands maps each command name to a function that parses its arguments.
var commands = map[string]func(args []string) (action, error){
	"tenant create": parseTenantCreate,
	"apikey create": parseAPIKeyCreate,
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run executes the command in args and returns the process exit code:
// 0 on success, 1 if the command fails, 2 on a usage error. Configuration is
// read through getenv.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}

	name, args := splitCommand(args)
	parse, ok := commands[name]
	if !ok {
		fmt.Fprintf(stderr, "herald-admin: unknown command %q\n\n%s", name, usage)
		return 2
	}
	act, err := parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "herald-admin: %s: %v\n", name, err)
		return 2
	}

	dbURL := getenv("HERALD_DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(stderr, "herald-admin: HERALD_DATABASE_URL is not set")
		return 1
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(stderr, "herald-admin: connect to database: %v\n", err)
		return 1
	}
	defer pool.Close()

	if err := act(ctx, ingest.NewStore(pool), stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "herald-admin: %v\n", err)
		return 1
	}
	return 0
}

// splitCommand splits args into a command name and that command's
// arguments. Commands are a noun and a verb ("tenant create").
func splitCommand(args []string) (name string, rest []string) {
	if len(args) < 2 {
		return args[0], nil
	}
	return args[0] + " " + args[1], args[2:]
}

func parseTenantCreate(args []string) (action, error) {
	fs := newFlagSet()
	name := fs.String("name", "", "")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	if *name == "" {
		return nil, errors.New("--name is required")
	}

	return func(ctx context.Context, store *ingest.Store, stdout, _ io.Writer) error {
		tenant, err := store.CreateTenant(ctx, *name)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, tenant.ID)
		return nil
	}, nil
}

func parseAPIKeyCreate(args []string) (action, error) {
	fs := newFlagSet()
	tenant := fs.String("tenant", "", "")
	if err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	if *tenant == "" {
		return nil, errors.New("--tenant is required")
	}
	tenantID, err := uuid.Parse(*tenant)
	if err != nil {
		return nil, fmt.Errorf("--tenant %q is not a UUID", *tenant)
	}

	return func(ctx context.Context, store *ingest.Store, stdout, stderr io.Writer) error {
		key, err := store.CreateAPIKey(ctx, tenantID)
		if err != nil {
			return err
		}
		// The key goes to stdout alone, so it can be piped into a secret
		// store; the note goes to stderr.
		fmt.Fprintln(stdout, key)
		fmt.Fprintln(stderr, "herald-admin: this API key is shown only once; store it now")
		return nil
	}, nil
}

// newFlagSet returns a flag set that reports errors to its caller instead of
// printing them, so run decides how they are shown.
func newFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseFlags parses args into fs and rejects positional arguments.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return nil
}
