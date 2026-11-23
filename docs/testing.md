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

- **Check:** what the scenario's fake Endpoints receive (the edge recording), and counts and timing from the spans. The scenario is generated traffic from a seed, with Endpoints that misbehave as real ones do and faults in the stack.
- **Compare against:** a committed baseline for what doesn't depend on the machine, and a run of the `main` build on the same runner for timing. A recording keeps whatever the recorded build did, bugs included.
- **Catch:** any change in behaviour, intended or not, for the inputs the scenario generates. An intended change shows up as a baseline diff the reviewer approves.
- **Can't catch:** a bug the baseline already holds, or inputs the scenario doesn't generate. It says that behaviour changed, not that it is right.
- **Run:** `make scenario-compare`; `make scenario-record` records a new baseline. CI runs it on every PR once #81 lands.

## Scenario invariants

- **Check:** rules that hold on any build, after every scenario run: at the edge (signatures, `webhook-id`, payloads, fan-out, the per-Endpoint cap, the retry schedule), in final state in Postgres (every Delivery finished and agreeing with the edge, the Outbox drained, duplicate protection), in the spans (complete traces, no orphans, no unexpected errors, lag and lateness under fixed limits), and in the goroutine profiles (no leaks).
- **Compare against:** an absolute rule. There is no baseline, and a rule is never changed to accept a new behaviour.
- **Catch:** a rule broken anywhere in the generated traffic, including while faults run, where the end-to-end tests don't look. A failure names what broke: the Delivery, Message, span or goroutine stack, and the rule.
- **Can't catch:** wrong behaviour that breaks no stated rule, which is what the comparison is for. Every check fails on missing data, so a check can't pass on nothing; but a rule nobody wrote down isn't checked.
- **Run:** after every scenario run, in the same `make scenario-compare` or `make scenario-record`, so on every PR once #81 lands.

## Load test

- **Check:** the scenario scaled up, with several replicas of each service.
- **Compare against:** nothing. It measures handoff lag, Attempt lateness and throughput, to settle the tuning questions in [docs/design.md](design.md) (Deferred).
- **Catch:** limits that only show under load.
- **Can't catch:** regressions on a PR: shared runners make its timings too noisy to gate on.
- **Run:** `make loadtest`, on demand, once #82 adds it.

## Where a new check belongs

- A rule about one function or package, decidable without the stack: a **unit** or **integration** test.
- A rule about the whole system for one case you can describe: an **end-to-end** test.
- A rule that must hold for every Delivery, Message or span the scenario produces, on any build: a **scenario invariant**.
- Behaviour that is right because it is what Herald does now, not because a rule says so (which headers, how many Attempts, how they are timed): the **behaviour comparison** already records it. A bug that reached `main` adds its case to the scenario.
- A question about capacity or tuning: the **load test**.
