// Command scenario-traffic prints synthetic traffic, drawn from a seed, as a
// traffic file: a draft to start from. The scenario runs only its committed
// traffic file, which is written by a person or captured from real traffic,
// and this command never writes it. Run it with
// `make scenario-traffic SEED=<n>`.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/madskrogh/herald/internal/scenario"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "scenario-traffic:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("scenario-traffic", flag.ContinueOnError)
	seed := fs.Uint64("seed", 0, "the seed to draw the traffic from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return scenario.WriteTraffic(out, scenario.Generate(*seed))
}
