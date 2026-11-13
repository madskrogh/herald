// Package natstest runs one NATS container per test package and gives each
// test subjects of its own, so integration tests can run in parallel against
// real NATS.
package natstest

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
)

// image matches the NATS version in compose.yaml.
const image = "nats:2.12-alpine"

// serverURL connects to the container. It is empty until Start has started
// the container.
var serverURL string

// Start starts the package's NATS container. It is a testmain.Start.
func Start(ctx context.Context) (stop func(), err error) {
	ctr, err := tcnats.Run(ctx, image)
	stop = func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			fmt.Fprintln(os.Stderr, "natstest: terminate container:", err)
		}
	}
	if err != nil {
		return stop, fmt.Errorf("natstest: start nats: %w", err)
	}
	serverURL, err = ctr.ConnectionString(ctx)
	if err != nil {
		return stop, fmt.Errorf("natstest: connection string: %w", err)
	}
	return stop, nil
}

// URL returns the NATS server's URL.
func URL(t *testing.T) string {
	t.Helper()
	if serverURL == "" {
		t.Fatal("natstest: no NATS container; pass natstest.Start to testmain.Run in TestMain")
	}
	return serverURL
}

// Connect returns a connection to the NATS server, closed when t finishes.
func Connect(t *testing.T) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(URL(t))
	if err != nil {
		t.Fatalf("natstest: connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// Subject returns a subject no other test uses.
func Subject(t *testing.T) string {
	t.Helper()
	return "test." + rand.Text()
}
