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
- Use a fake clock or inject `time.Now` for retry-schedule and expiry logic; never `time.Sleep` to wait for a state change.
