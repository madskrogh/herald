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
	conns := make([]*pgxpool.Conn, pool.Config().MaxConns)
	for i := range conns {
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatalf("acquire connection: %v", err)
		}
		conns[i] = conn
	}
	for _, conn := range conns {
		conn.Release()
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
