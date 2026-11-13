package ingest_test

import (
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// concurrently makes the n calls call(0) to call(n-1) at once, on connections
// from pool, and returns when all have returned.
func concurrently(t *testing.T, pool *pgxpool.Pool, n int, call func(i int)) {
	t.Helper()
	// Open the pool's connections first. Otherwise opening them staggers the
	// calls by more than a call takes, and they run one after another.
	// They are all released before Fatalf: the pool's cleanup waits for every
	// connection to come back.
	if err := warmUp(t, pool); err != nil {
		t.Fatalf("acquire connection: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			call(i)
		})
	}
	close(start)
	wg.Wait()
}

// warmUp opens all of pool's connections, by acquiring each, and releases
// them.
func warmUp(t *testing.T, pool *pgxpool.Pool) error {
	var conns []*pgxpool.Conn
	defer func() {
		for _, conn := range conns {
			conn.Release()
		}
	}()
	for range pool.Config().MaxConns {
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			return err
		}
		conns = append(conns, conn)
	}
	return nil
}
