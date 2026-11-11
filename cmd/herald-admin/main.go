// Command herald-admin is the operator CLI for Herald. It manages Tenants and
// their API keys by connecting directly to the ingest schema.
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = `usage: herald-admin <command> [arguments]

herald-admin is the operator CLI for Herald. It connects directly to the
ingest schema.

Commands:
  help    Show this help
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command in args and returns the process exit code:
// 0 on success, 2 on a usage error.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "herald-admin: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
