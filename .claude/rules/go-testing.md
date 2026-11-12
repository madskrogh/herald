---
paths:
  - "**/*_test.go"
  - "**/testdata/**"
---

# Go tests

- Use the standard `testing` package and `github.com/google/go-cmp/cmp` for comparisons (`if diff := cmp.Diff(want, got); diff != "" { t.Errorf("... (-want +got):\n%s", diff) }`). No testify, no assertion libraries.
- Table-driven tests with `t.Run` subtests when there is more than one case. Call `t.Parallel()` in tests and subtests unless they share state that makes it unsafe; say why in a comment when you leave it out.
- Tests always run with `-race`.
- Expected values are literals or worked examples, never recomputed the way the code computes them.

## Postgres and NATS

- Integration tests run against real Postgres and NATS through testcontainers-go. Never mock either, and never fake them with in-memory stand-ins.
- Start containers once per package in `TestMain`, through the shared test helpers, and give each test its own isolated state (a fresh database or schema, unique NATS subjects) so tests can run in parallel.
- Mark integration tests so `go test -short` skips them: `if testing.Short() { t.Skip("integration") }`.

## Specific techniques

- **Fuzz tests** (`FuzzXxx`) for anything that parses untrusted input, starting with webhook signature headers. Keep interesting inputs in `testdata/fuzz/`.
- **Positive controls** for tests that assert something is absent (a key is not stored, a secret is not logged). Run the same check on input where it is present, and expect it to fire. Without that, a check that can never fire passes for the wrong reason: a `bytea` column shows as hex in a row's text form, so searching that text for the plain key finds nothing even when the key is stored.
- **Golden files** for API responses: `testdata/<name>.golden`, rewritten with an `-update` flag. Review golden diffs like code.
- **Never `time.Sleep`** to wait for a state change.
- **Concurrency and time without Postgres or NATS.** When a test involves goroutines, channels, timers or deadlines and the code under test touches no Postgres, NATS or real socket, run it inside `testing/synctest`. Time is fake there, and `synctest.Test` waits for every goroutine in the bubble to exit: one left durably blocked (on a bubble channel, `sync.WaitGroup`, `sync.Cond`, `time.Sleep`) fails the test as a deadlock, but one stuck on a mutex or I/O makes the test hang instead. Wait with `synctest.Wait()`, never by polling or sleeping.
  - Fake the network as the synctest docs do: `net.Pipe` connections through `http.Transport.DialContext`, so the real HTTP client runs. Real sockets, including `httptest.Server`, keep the bubble from going idle, so fake time never advances.
  - A concurrency component (a worker pool, a shutdown sequence) may take its source of work as a function, so its in-process logic can be tested this way. That isn't faking Postgres: SQL behaviour is still tested against real Postgres.
- **Time with Postgres or NATS.** The current time is a parameter (see `sql.md`). Polling loops expose a single round (`RunOnce(ctx, now)`) that tests call directly, and the loop around it stays thin.
- **goleak** (`go.uber.org/goleak`) in packages that start goroutines that run in the background (the relay, the delivery workers): `goleak.VerifyTestMain(m)` in `TestMain` (per-test `VerifyNone` doesn't work with `t.Parallel`). Ignore third-party goroutines with `goleak.IgnoreTopFunction("<fully qualified function>")`, with a comment saying where each comes from.
