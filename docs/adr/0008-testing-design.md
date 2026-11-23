# Testing: expected results, a behaviour comparison, and invariants

Herald is checked in six ways, and each exists because the others can't catch what it catches. [docs/testing.md](../testing.md) says how they differ and where a new check belongs; this ADR says why each exists.

- **Unit tests** check pure logic, such as filters, the retry schedule and signing, against expected results. They answer in milliseconds for an input someone thought of.
- **Integration tests** run one package against real Postgres and NATS, never mocks of either. Herald's correctness rests on how they behave: SQL that does what its caller assumes, claims that stay apart between replicas, Batches neither lost nor doubled at the handoff. A mock would assume that behaviour instead of checking it.
- **End-to-end tests** run the built services together, through the Tenant API, against fake Endpoints, for one hand-picked case each: retries, duplicate protection, isolation between Endpoints, one Message's trace. They catch faults in how the services are wired, configured and deployed.
- **The behaviour comparison** shows when what Herald does changes, intended or not.
- **Scenario invariants** check rules that hold on any build, across all the traffic the comparison generates.
- **Load tests** measure capacity, on demand.

## The behaviour comparison

The other tests check what Herald should do, for the cases someone wrote down. The comparison checks what Herald does, and shows when that changes. Herald has no production traffic, so a scenario of realistic use stands in for it. It covers only the inputs it generates: it catches unintended changes, not inputs nobody imagined. To cover as much as it can:

- **Generated traffic.** Applications, Endpoints with varied filters, and Messages with varied Event types, payload sizes and Idempotency-Keys. The same traffic can be run again exactly, so two runs differ only where Herald does.
- **Endpoints that misbehave as real ones do:** success, failures then success, slow responses, timeouts, connection resets, oversized or malformed responses, redirects, TLS errors, and Endpoints that flap.
- **Faults during the run:** a dispatcher restarted, NATS paused, Postgres slowed down.
- **Every bug becomes a case.** A fix for a bug that reached `main` adds the case that shows it to the scenario, so the scenario grows from what actually went wrong.

### Two witnesses

- **The edge recording:** what the fake Endpoints received (Endpoint, headers, body, order, time since the Delivery's first Attempt) and each Delivery's final state. What varies between runs (IDs, timestamps, retry jitter) is normalised, and signatures are verified rather than compared byte for byte. It is taken outside Herald, so a change to Herald's instrumentation can't change it.
- **Telemetry statistics,** per run, from the spans: Attempts by outcome, retry timing, Attempt latency and handoff lag. They show what the edge can't see, such as how long handoff took.

### What each is compared with

What doesn't depend on the machine, the edge recording and counts such as Attempts by outcome, is compared with committed baselines. A difference fails the comparison unless the change updates the baseline, so an intended change shows up as a diff the reviewer approves, and a change that should alter nothing must leave the baselines alone.

Timing depends on the machine. The edge recording's time since a Delivery's first Attempt is compared with the baseline only within a margin wide enough for any machine, so it catches a large change, such as a retry delay of hours, and nothing finer. A baseline recorded elsewhere would need margins too wide to catch a regression in the rest, so the telemetry statistics' timing is compared with a run of the `main` build on the same runner at the same time, each statistic within a margin.

A recording keeps whatever the recorded build did, bugs included. It shows change, not correctness.

## Scenario invariants

After every scenario run, rules that hold on any build are checked: at the edge, in Herald's final state, in the spans, and in each service's resource use. They have no baseline and are never changed to accept a new behaviour, so a violation is a bug, not a change to approve. They catch a rule broken anywhere in the generated traffic, including while faults run, where the end-to-end tests don't look. Every check fails on missing data: a query that returns nothing is a failure, never a pass.

## Load tests

The scenario scaled up, with several replicas of each service, on demand. They settle tuning and capacity questions. They don't run on every change, because shared runners make timings too noisy to gate on.

## Considered Options

- **A scripted scenario, recorded at both edges and run again against a later build**: replaced. A fixed set of Tenant calls with a response script per Endpoint covers one script's cases. Generated traffic, misbehaving Endpoints and faults cover more of what real use would.
- **Telemetry as the only witness**: rejected. Spans are reported by the code under test, so a change to its instrumentation could change what the checks read. The edge recording can't be changed that way.
- **Timing statistics against a committed baseline**: rejected, for the margins it would need (above).
- **Load tests on every change**: rejected, for noisy runners (above).

## Consequences

- Kind, in [ADR 0007](0007-code-regenerated-from-the-spec.md)'s terms: architecture. Regenerated checks keep these kinds of tests, the two witnesses, and what each is compared with. The tools, how faults are injected, and how the recording is normalised are their own choice. The invariants' rules, and the margins and limits they hold Herald to, belong in the spec.
- Every PR is to run the end-to-end tests, the comparison and the invariants, which adds the full stack and two scenario runs (the candidate, and `main` for timing) to its CI time. The telemetry statistics, the `main` run and running all of it on every PR come with #81.
- A run needs the stack to itself: its faults restart and pause services that any other test on the same stack would be using.
- A reviewer reads a baseline diff as a behaviour change to approve, not as noise.
