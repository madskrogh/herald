// Package testmain runs a test package's tests with the containers they need,
// such as Postgres (pgtest) and NATS (natstest), started once for the
// package.
package testmain

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"testing"
)

// Start starts a container for a test package and returns a function that
// removes it. It returns a stop function even when it fails, if it got as far
// as creating the container.
type Start func(ctx context.Context) (stop func(), err error)

// Run starts the containers, runs the package's tests, removes the
// containers and exits. Call it from TestMain. With -short it runs the tests
// without containers, and integration tests skip themselves.
func Run(m *testing.M, starts ...Start) {
	os.Exit(RunCode(m, starts...))
}

// RunCode is Run without the exit: it returns the exit code. It is for a
// TestMain that checks something once the containers are gone, as
// goleak.VerifyTestMain does for leaked goroutines.
func RunCode(m *testing.M, starts ...Start) int {
	return run(m, starts)
}

func run(m *testing.M, starts []Start) int {
	flag.Parse()
	if testing.Short() {
		return m.Run()
	}

	ctx := context.Background()
	var stops []func()
	defer func() {
		for _, stop := range slices.Backward(stops) {
			stop()
		}
	}()
	for _, start := range starts {
		stop, err := start(ctx)
		if stop != nil {
			stops = append(stops, stop)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "testmain:", err)
			return 1
		}
	}
	return m.Run()
}
