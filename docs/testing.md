# Testing

Herald has six kinds of tests. They differ in what they run against and in what decides whether they pass: an expected result written in the test, a committed baseline, a run of `main` on the same runner, or a rule that holds on any build. This page is for deciding where a new check belongs. [ADR 0008](adr/0008-testing-design.md) says why each kind exists.

| Kind | Runs against | Passes when it matches | Runs |
|---|---|---|---|
| Unit | the code, in process | the expected result | `make test`, every PR |
| Integration | real Postgres and NATS in containers | the expected result | `make test`, every PR |
| End-to-end | the compose stack | the expected result | `make e2e`, on `main`; on every PR once #81 lands |
| Behaviour comparison | the compose stack, under the scenario | a committed baseline, or a `main` run | `make scenario-compare`; on every PR once #81 lands |
| Scenario invariants | the compose stack, after the scenario | a rule that holds on any build | after every scenario run; on every PR once #81 lands |
| Load test | the stack scaled up, under the scenario scaled up | nothing: it measures | `make loadtest`, on demand (#82) |

## Unit tests

- **Check:** pure logic, such as filters, the retry schedule, signing and the scenario's own checks.
- **Compare against:** the expected result, written in the test as a literal or a worked example.
- **Catch:** a wrong answer for an input someone thought of, in milliseconds.
- **Can't catch:** anything that depends on Postgres, NATS, the network or another service.
- **Run:** `make test`, on every PR.

## Integration tests

- **Check:** one package against real Postgres and NATS, started with testcontainers-go: the queries, the Outbox, the handoff, claiming and recording Attempts.
- **Compare against:** the expected result.
- **Catch:** SQL that doesn't do what its Go caller assumes, locking and concurrency between replicas, messages lost or doubled at the handoff.
- **Can't catch:** a fault in how the services are wired together, configured or deployed.
- **Run:** `make test`, on every PR. `go test -short` skips them.

## End-to-end tests

- **Check:** the built images in the compose stack, through the Tenant API, delivering to fake Endpoints served by the test.
- **Compare against:** the expected result for one hand-picked case per test: retries, duplicate protection, isolation between Endpoints, one Message's trace.
- **Catch:** a rule broken in the whole system, for the case the test picks.
- **Can't catch:** the cases nobody picked, faults in the stack, or a change in behaviour no test asserts on.
- **Run:** `make e2e` against a stack started with `make up`, and in CI on `main`, and on every PR once #81 lands.

## Behaviour comparison

- **Check:** what the scenario's fake Endpoints receive (the edge recording), and counts and timing from the spans. The scenario runs the committed traffic file, `internal/scenario/testdata/traffic.json`: Applications, Endpoints with their filters and how each answers, misbehaving as real ones do, and Messages sent in phases with faults in the stack. The traffic is edited by hand. A unit test holds the file to the rules the comparison depends on: every Endpoint behaviour and fault, repeated and conflicting Idempotency-Keys, and no more Deliveries to a slow or retrying Endpoint in a phase than keep its timing steady. `make scenario-traffic SEED=<n>` prints synthetic traffic, drawn from a seed, as a draft to copy from; nothing runs it. The baseline names the traffic file and its SHA-256, so a traffic change without a new baseline fails the comparison.
- **Compare against:** a committed baseline for what doesn't depend on the machine, and a run of the `main` build on the same runner for timing. A recording keeps whatever the recorded build did, bugs included.
- **Catch:** any change in behaviour, intended or not, for the inputs in the traffic file. An intended change shows up as a baseline diff the reviewer approves.
- **Can't catch:** a bug the baseline already holds, or inputs the traffic file doesn't have. It says that behaviour changed, not that it is right.
- **Run:** `make scenario-compare`; `make scenario-record` records a new baseline from the traffic file the baseline names. CI runs it on every PR once #81 lands.

## Scenario invariants

- **Check:** the spec's requirements, after every scenario run, at the edge (what the Endpoints received), in final state in Postgres, and in the spans. Each check names the requirements it holds Herald to, in its doc comment in `internal/scenario` (`CheckEdge`, `CheckFinalState`, `CheckSpans`). Its filter matching and retry schedule are its own, written from [the spec](../spec/README.md) and tested against its vectors, and its limits are compared with [spec/limits.md](../spec/limits.md). One check isn't a requirement: each service's goroutines return to their idle set, a Go-only way of showing the service holds nothing once the scenario ends, which serves the resource requirements stated without the language, such as R-87 ([ADR 0007](adr/0007-code-regenerated-from-the-spec.md)).
- **Compare against:** the spec's requirements. There is no baseline, and a check changes only when its requirement does.
- **Catch:** a rule broken anywhere in the traffic, including while faults run, where the end-to-end tests don't look. A failure names what broke: the Delivery, Message, span or goroutine stack, and the rule.
- **Can't catch:** wrong behaviour that breaks no stated rule, which is what the comparison is for. Every check fails on missing data, so a check can't pass on nothing; but a rule nobody wrote down isn't checked.
- **Run:** after every scenario run, in the same `make scenario-compare` or `make scenario-record`, so on every PR once #81 lands.

## Load test

- **Check:** the scenario scaled up, with several replicas of each service.
- **Compare against:** nothing. It measures handoff lag, Attempt lateness and throughput, to settle the tuning questions that wait on load (#124, #125, #126).
- **Catch:** limits that only show under load.
- **Can't catch:** regressions on a PR: shared runners make its timings too noisy to gate on.
- **Run:** `make loadtest`, on demand, once #82 adds it.

## Profiles

Not a test, and not a gate: CPU time on a small, I/O-bound scenario is noise. `make profile` captures CPU, heap and allocation profiles and an execution trace from two builds while the scenario runs against both, and shows how they differ (`go tool pprof -diff_base`). Use it to explain a difference the comparison found, or to back a claim about performance.

## CI

On every PR, and on `main` after each merge: `make ci` (`golangci-lint`, `go test -race` with the unit and integration tests, `buf lint`, `buf breaking` against `main`, and a check that the sqlc and protobuf generated code is up to date), and a Docker image build for each service. On `main` only, the end-to-end tests against the compose stack. Once #81 lands, every PR also runs the end-to-end tests, the behaviour comparison and the scenario invariants. `.github/workflows/ci.yml` is the record.

## Where a new check belongs

- A rule about one function or package, decidable without the stack: a **unit** or **integration** test.
- A rule about the whole system for one case you can describe: an **end-to-end** test.
- A rule that must hold for every Delivery, Message or span the scenario produces, on any build: a **scenario invariant**.
- Behaviour that is right because it is what Herald does now, not because a rule says so (which headers, how many Attempts, how they are timed): the **behaviour comparison** already records it. A bug that reached `main` adds its case to the traffic file.
- A question about capacity or tuning: the **load test**.
