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
- **Golden files** for API responses: `testdata/<name>.golden`, rewritten with an `-update` flag. Review golden diffs like code.
- **Never `time.Sleep`** to wait for a state change.
- **Concurrency and time without Postgres or NATS.** When a test involves goroutines, channels, timers or deadlines and the code under test touches no Postgres, NATS or real socket, run it inside `testing/synctest`: time is fake, and the test fails on deadlocks and on goroutines left blocked. Wait with `synctest.Wait()`, never by polling or sleeping. Fake HTTP receivers with an in-process `http.RoundTripper`, because real sockets (including `httptest.Server`) stop fake time from advancing.
  - A concurrency component (a worker pool, a shutdown sequence) may take its source of work as a function, so its in-process logic can be tested this way. That isn't faking Postgres: SQL behaviour is still tested against real Postgres.
- **Time with Postgres or NATS.** The current time is a parameter (see `sql.md`). Polling loops expose a single round (`RunOnce(ctx, now)`) that tests call directly, and the loop around it stays thin.
- **goleak** (`go.uber.org/goleak`) in packages that start goroutines that run in the background (the relay, the delivery workers): `goleak.VerifyTestMain(m)` in `TestMain`. Ignore third-party goroutines by name only, with a comment saying where each comes from.
