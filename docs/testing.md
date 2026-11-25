# Testing

Herald has six kinds of tests. They differ in what they run against and in what decides whether they pass: an expected result written in the test, a committed baseline, a run of `main` on the same runner, or a rule that holds on any build. This page is for deciding where a new check belongs. [ADR 0008](adr/0008-testing-design.md) says why each kind exists.

| Kind | Runs against | Passes when it matches | Runs |
|---|---|---|---|
| Unit | the code, in process | the expected result | `make test`, every PR |
| Integration | real Postgres and NATS in containers | the expected result | `make test`, every PR |
| End-to-end | the compose stack | the expected result | `make e2e`, every PR |
| Behaviour comparison | the compose stack, under the scenario | a committed baseline, or a `main` run | `make scenario-compare` and `make scenario-timing`, every PR |
| Scenario invariants | the compose stack, after the scenario | a rule that holds on any build | after every scenario run, every PR |
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
- **Run:** `make e2e` against a stack started with `make up`, and in CI on every PR and on `main`.

## Behaviour comparison

- **Check:** what the scenario's fake Endpoints receive (the edge recording), and counts and timing from the spans, when the scenario runs the committed traffic file (below).
- **Compare against:** a committed baseline for what doesn't depend on the machine, and a run of the `main` build on the same runner for timing. A recording keeps whatever the recorded build did, bugs included.
- **Catch:** any change in behaviour, intended or not, for the inputs in the traffic file. An intended change shows up as a baseline diff the reviewer approves.
- **Can't catch:** a bug the baseline already holds, or inputs the traffic file doesn't have. It says that behaviour changed, not that it is right.
- **Run:** `make scenario-compare` compares with the committed baselines; `make scenario-record` records new ones from the traffic file the baseline names. `make scenario-timing BASE=<checkout>` runs the scenario on this checkout's stack and the base checkout's at the same time, compares the timing, and is also `make scenario-compare` for this checkout. CI runs `make scenario-timing` on every PR, with `main` as the base, and on `main` after each merge, with the `main` before it.

### The two witnesses

- **The edge recording** (`internal/scenario/testdata/baseline.json`): what the Endpoints received, compared with the baseline. Its timing, each Attempt's time since its Delivery's first, is compared within a margin wide enough for any machine (below).
- **Telemetry statistics**, read from the run's spans, using only the span names, attribute keys and outcomes [spec/span-schema.md](../spec/span-schema.md) declares, and only for Messages sent in a phase without a fault, since what a fault does depends on when it takes hold. Two kinds:
  - **Counts** (`internal/scenario/testdata/counts.json`): spans by name and `herald.outcome`, and Tenant API calls by method and outcome. Calls are counted in every phase: each is a call the traffic file makes, and one the stack fails, under a fault or not, fails the run instead. They don't depend on the machine, so they are compared with a committed baseline, which names its traffic file as the edge baseline does and is recorded with it. Batches aren't counted: how Messages are batched depends on when they arrive.
  - **Timing**: handoff lag (from a Message's Tenant API call ending to its handoff starting), Attempt latency (`herald.latency_ms`, except for Endpoints the scenario makes slow or time out, whose latency is the harness's), and the lateness of retries (`herald.lateness_ms`), each as its 50th and 95th percentile. A first Attempt's lateness isn't one: while Deliveries are stored on one polling schedule and Attempts made on another, it is how far apart the two schedules happen to be, nearly the same for every Delivery of a run and different in the next. They depend on the machine, so each is compared with a run of the base build on the same runner at the same time, within its margin (below). The report lists both runs' values and sample counts, and every span in each build's ClickHouse is saved beside it, as Parquet, for SQL over the two.

### Timing margins

How far each timing statistic may be from what it is compared with, either way: a percentage of the value compared with, plus a constant. A margin is a tolerance of the comparison, not something Herald promises, so it is here rather than in the spec. A unit test compares the code's margins with this table.

| Statistic | Margin | Reason |
|---|---|---|
| time since a Delivery's first Attempt | 25% + 1000 ms | The edge recording, against the committed baseline. The retry schedule's jitter moves each delay by up to 10% either way, in the baseline's run as well as this one, so the two can be 20% of the schedule apart, up to 22% of the baseline's time; the constant covers the delivery workers' polling and the machine. With the local stack's retry scale, a change to a retry delay of an hour or more fails the comparison, and shorter ones are lost in the margin (ADR 0008). |
| handoff lag p50 | 10% + 50 ms | Against the base's run, as every row below. Five CI runs of the same build (#154) were 234–267 ms and the two runs at most 23 ms apart. Lag is set by the relay's 500 ms polling, so a slower or less frequent relay moves it by hundreds of milliseconds, beyond the margin's 75 or so. |
| handoff lag p95 | 10% + 50 ms | The same runs were 460–489 ms and at most 29 ms apart: the margin is about three times that. |
| Attempt latency p50 | 25% + 20 ms | The fake Endpoints answer at once, so it is 1 ms on CI, the same in both runs. The constant is for the runner, and is the smallest slowdown of every Attempt it catches. |
| Attempt latency p95 | 25% + 20 ms | The same runs were 5–9 ms and at most 2 ms apart. |
| retry lateness p50 | 10% + 25 ms | The same runs were 438–446 ms and at most 5 ms apart. Lateness is set by the delivery workers' 500 ms polling, so a change to it moves lateness by more than the margin's 70 or so. |
| retry lateness p95 | 10% + 25 ms | The same runs were 500–508 ms and at most 5 ms apart. |

When the timing comparison fails, its report says which statistic moved, and holds the diff of CPU, allocation, heap, goroutine and blocking profiles taken from both builds while they ran (`scripts/profile.sh`), to show where the time went. The profiles and execution traces are uploaded with the services' logs.

### The traffic file and the baseline

The comparison rests on two committed files, an input and an output.

- **The traffic file** (`internal/scenario/testdata/traffic.json`) is the input: what the scenario sends. Applications, Endpoints with their filters and how each answers, misbehaving as real ones do, and Messages sent in phases with faults in the stack. It says nothing about how Herald behaves. It may be written by a person, or captured from real traffic, such as production. `make scenario-traffic SEED=<n>` prints synthetic traffic, drawn from a seed, as a draft to start from; the scenario never runs it, and it never writes the committed file. A unit test holds whatever file is committed to the rules the comparison depends on: every Endpoint behaviour and fault, repeated and conflicting Idempotency-Keys, and no more Deliveries to a slow or retrying Endpoint in a phase than keep its timing steady.
- **The baseline** (`internal/scenario/testdata/baseline.json`) is the output: what the Endpoints received when that traffic ran. What the Tenant API answered to each Message, and each Delivery's Attempts in order, with their headers, body, signature and rough timing, and how it ended. It is recorded, never written by hand: by `make scenario-record`, which runs the traffic against a build, or captured together with the traffic, from the same real traffic.
- **How they are tied.** The baseline names its traffic file and that file's SHA-256 as they were when it was recorded. The comparison runs the traffic file the baseline names, and a traffic file changed since fails it with that difference first. So a change to the traffic comes with a new baseline in the same PR, and a baseline diff with no traffic change is a change in Herald's behaviour, for the reviewer to approve.

## Scenario invariants

- **Check:** the spec's requirements, after every scenario run, at the edge (what the Endpoints received), in final state in Postgres, and in the spans. Each check names the requirements it holds Herald to, in its doc comment in `internal/scenario` (`CheckEdge`, `CheckFinalState`, `CheckSpans`). Its filter matching and retry schedule are its own, written from [the spec](../spec/README.md) and tested against its vectors, and its limits are compared with [spec/limits.md](../spec/limits.md). One check isn't a requirement: each service's goroutines return to their idle set, a Go-only way of showing the service holds nothing once the scenario ends, which serves the resource requirements stated without the language, such as R-87 ([ADR 0007](adr/0007-code-regenerated-from-the-spec.md)).
- **Compare against:** the spec's requirements. There is no baseline, and a check changes only when its requirement does.
- **Catch:** a rule broken anywhere in the traffic, including while faults run, where the end-to-end tests don't look. A failure names what broke: the Delivery, Message, span or goroutine stack, and the rule.
- **Can't catch:** wrong behaviour that breaks no stated rule, which is what the comparison is for. Every check fails on missing data, so a check can't pass on nothing; but a rule nobody wrote down isn't checked.
- **Run:** after every scenario run, in the same `make scenario-compare`, `make scenario-record` or `make scenario-timing` (on the candidate's run only), so on every PR.

## The scenario report

Every scenario run with `OUT` writes one report, `OUT/report.md`, with everything the run found, so a PR states its result from that file and CI shows it on the run's page:

- **Behaviour comparison:** "no differences", or each difference by Delivery and field against the edge baseline, and by kind of span against the counts baseline, as `make scenario-compare` reports them.
- **Scenario invariants:** each one, held or broken, and each violation of a broken one.
- **Timing comparison** (`make scenario-timing` only): each statistic of both runs within its margin or not, and the profile diff when one isn't.

A run that stops before it finishes still writes the report: it says why the run stopped, and what it found until then. `make scenario-compare OUT=<dir>` writes the same report without the timing comparison. The golden files in `internal/scenario/testdata/` show it for a run that passes, one with a difference, a broken invariant and a statistic beyond its margin, one that stopped, and one whose timing couldn't be compared.

## Check changes

A PR may re-record the committed baselines with the change that moves them, but a change to a check goes in a PR of its own: whoever changes behaviour must not also change what judges it. The case this guards against is the quiet one, a margin loosened beside the change that would have failed it.

- **The check files** are the scenario's checks, comparisons, margins and invariants, the timing comparison, the queries the checks read with (so a renamed span attribute's queries are among them), the scripts that run them, this page, and the CI workflow, so a PR can't switch the job off. The three baselines (`baseline.json`, `counts.json` and `traffic.json` in `internal/scenario/testdata/`) are not check files, and neither are the end-to-end tests' Go files: a feature adds its end-to-end tests with its code. The list is kept in one place, `scripts/checks-apart.sh`.
- **The job.** On every PR, CI runs `scripts/checks-apart.sh`, which fails when the PR changes a check file together with any other file, and lists both sets. A file renamed counts at both paths. Run it before pushing with `scripts/checks-apart.sh origin/main`.
- **A change that needs both**, such as a feature that needs a new invariant, is two PRs: the check first, then the change.

## Load test

- **Check:** the scenario scaled up, with several replicas of each service.
- **Compare against:** nothing. It measures handoff lag, Attempt lateness and throughput, to settle the tuning questions that wait on load (#124, #125, #126).
- **Catch:** limits that only show under load.
- **Can't catch:** regressions on a PR: shared runners make its timings too noisy to gate on.
- **Run:** `make loadtest`, on demand, once #82 adds it.

## Profiles

Not a test, and not a gate: CPU time on a small, I/O-bound scenario is noise. `make profile` captures CPU, heap and allocation profiles and an execution trace from two builds while the scenario runs against both, and shows how they differ (`go tool pprof -diff_base`). Use it to explain a difference the comparison found, or to back a claim about performance. `make scenario-timing` takes the same profiles during its runs, and puts their diff in its report when the timing comparison fails.

## CI

On every PR: the check-changes job (above), which keeps a change to a check out of a PR that changes anything else. On every PR, and on `main` after each merge: `make ci` (`golangci-lint`, `go test -race` with the unit and integration tests, `buf lint`, `buf breaking` against `main`, and a check that the sqlc and protobuf generated code is up to date), a Docker image build for each service, the end-to-end tests against the compose stack, and `make scenario-timing`: the behaviour comparison and the scenario invariants on the candidate's stack, with the base build's stack running the scenario beside it for the timing comparison. The scenario report (above) is on the run's page. On a failure, the services' logs, the report, the profiles and both runs' spans are uploaded with the run. `.github/workflows/ci.yml` is the record.

## Where a new check belongs

- A rule about one function or package, decidable without the stack: a **unit** or **integration** test.
- A rule about the whole system for one case you can describe: an **end-to-end** test.
- A rule that must hold for every Delivery, Message or span the scenario produces, on any build: a **scenario invariant**.
- Behaviour that is right because it is what Herald does now, not because a rule says so (which headers, how many Attempts, how they are timed): the **behaviour comparison** already records it. A bug that reached `main` adds its case to the traffic file.
- A question about capacity or tuning: the **load test**.
