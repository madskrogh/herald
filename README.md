# Herald

[![CI](https://github.com/madskrogh/herald/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/madskrogh/herald/actions/workflows/ci.yml?query=branch%3Amain)

Herald is two things. It is a webhook delivery service, built to production standards: a Tenant submits a Message once, and Herald works out which Endpoints want it, signs every Attempt with [Standard Webhooks](https://www.standardwebhooks.com/), and retries failures for about a day. And it is a working lab for agent-assisted development with guardrails: coding agents build it, and a human decides and merges.

The lab tests a claim: the code the workflow produces holds up, and what limits the pace is whether a human understands a change well enough to merge it. The spec and observability are meant to give that understanding, so that effort moves from reading PRs to maintaining the spec and watching production. It is a hypothesis, and the history can't confirm it yet ([case study](docs/case-study.md)).

## Highlights

**Delivery guarantees.** Delivery is at-least-once, with duplicate protection on every hop ([ADR 0001](docs/adr/0001-at-least-once-delivery.md)). herald-api writes a Message and its Deliveries in one transaction: the outbox. The handoff to herald-dispatcher is safe to repeat: a batch is marked handed off only once the dispatcher has committed it, and a resent batch inserts nothing new ([ADR 0002](docs/adr/0002-two-services-outbox-nats-handoff.md)). A request repeated with the same `Idempotency-Key` returns the original Message, and every Attempt of a Delivery carries the same `webhook-id`, so Endpoints can drop repeats. A slow Endpoint doesn't hold up Deliveries to other Endpoints ([isolation](spec/delivery.md#isolation)). And outside local development, Herald refuses to connect to private addresses, judging the address it actually dials, so an Endpoint can't make it a proxy into its own network ([Endpoint addresses](spec/security.md#endpoint-addresses)).

**The spec as the source of truth.** Every decision someone at Herald's boundaries could notice is a numbered requirement in [the spec](spec/README.md), with its status and its reason, and the ones easy to misread have test vectors. The aim is that Herald's code can be regenerated from the spec ([ADR 0007](docs/adr/0007-code-regenerated-from-the-spec.md)), so a reviewer checks a PR against the requirements it names instead of reading every line. Whether the spec is complete is untested. The regeneration audit would test it, scoped to what a milestone or a large change touched ([#164](https://github.com/madskrogh/herald/issues/164)), and it hasn't run.

**Behaviour comparison.** A change is trusted when its behaviour can be seen. Every PR runs a scenario of committed traffic, with faults injected, and compares what its Endpoints received with a committed baseline, and its telemetry with a committed baseline for counts and a run of `main` on the same runner for timing. Then it checks the scenario's invariants, rules that hold on any build ([testing](docs/testing.md)). In production, the same telemetry shows whether a feature works. [#75](https://github.com/madskrogh/herald/issues/75), still open, is to check each change against the scenario and its telemetry before it is pushed.

**The workflow and its evidence.** Each issue is built test-first in its own worktree, and reviewed for standards, spec and bugs before its PR opens ([how it's set up](docs/ai-workflow.md)). The [case study](docs/case-study.md) tests the claim against Herald's own history. In the PRs it read, the reviews raised about 20 findings per PR, and four problems were filed after merge. But nothing counts what the reviews missed, or how well the human understood a change, so the history can't confirm the hypothesis yet. [What would settle it](docs/case-study.md#what-would-settle-it) names the missing records. [#174](https://github.com/madskrogh/herald/issues/174), still open, is to start two of them: where each defect got in, and which corrections come from the human. From then on, the claim gets evidence as the work goes, not reconstructed afterwards.

## Try it

You need Go and Docker.

```sh
make up    # build and start the local stack
make e2e   # run the end-to-end tests against it
```

The end-to-end tests take about a minute and a half. They show that Herald:

- delivers a Message to its Endpoint, signed;
- retries a failed Attempt, and a timed-out one, with the same `webhook-id`;
- marks a Delivery failed when its retry schedule runs out;
- makes one Delivery when a request is repeated with the same `Idempotency-Key`;
- doesn't let a slow Endpoint delay another;
- records a Message as one trace, from the Tenant API call to every Attempt.

To send a Message by hand, watch it retry and find its trace, follow [Try it](docs/try-it.md). `make down` stops the stack and deletes its data.

## How it works

```
       Tenant                                                                    Endpoint
          │ POST /v1/messages                                            signed POST ▲
          │ Idempotency-Key                                               webhook-id │
┌─────────┼── herald-api ──────────────┐                ┌── herald-dispatcher ───────┼─────────┐
│         ▼                            │                │                            │         │
│  ┌──────────────┐   ┌────────────┐   │                │  ┌──────────────┐   ┌──────┴──────┐  │
│  │  Tenant API  │   │   relay    │   │    ┌──────┐    │  │    intake    │   │   workers   │  │
│  │ gRPC + REST  │   │polls outbox├───┼───►│ NATS │────┼─►│ stores batch │   │ ≤4/Endpoint │  │
│  │ API-key auth │   │ one per api│◄--┼----│      │◄---┼--│ commit, reply│   │ sign · retry│  │
│  └──────┬───────┘   └─────┬──────┘   │    └──────┘    │  └──────┬───────┘   └──────┬──────┘  │
└─────────┼─────────────────┼──────────┘                └─────────┼──────────────────┼─────────┘
          │ one transaction ▲ SKIP LOCKED claim                   │ ON CONFLICT      ▲ claim due
          ▼                 ▼ mark handed off                     ▼ DO NOTHING       ▼ Attempts
┌──────────────────────────────────────┐                ┌──────────────────────────────────────┐
│       Postgres · ingest schema       │                │      Postgres · delivery schema      │
│    Messages (outbox) · Deliveries    │                │        Deliveries · Attempts         │
└─────────┴────────────────────────────┘                └──────────────────────────────────────┘
          ▲ Tenants, API keys
          │
   ┌──────┴───────┐
   │ herald-admin │                                         ──► batch   ◄-- reply after commit
   └──────────────┘
```

Two services, each with its own schema. herald-api accepts a Message and writes it with its Deliveries in one transaction. A relay hands them to herald-dispatcher in batches over NATS. The dispatcher's workers make the Attempts, with a cap on how many are in flight to one Endpoint, and retry failures. Every Message is one trace, from the Tenant API call to its last Attempt, stored in ClickHouse.

Herald runs locally with `docker compose`, and later on a local Kubernetes cluster, and depends on no particular cloud provider. It is built to be run and read, not hosted. It has no Tenant self-service (Tenants are created with the `herald-admin` CLI), no portal for Applications (Tenants show signing secrets and delivery history in their own products), no ordered delivery, content-based routing or payload transforms, and no releases, hosting or marketing site.

## Status and roadmap

M1, the core delivery path, and M2, observability and behaviour comparison, are done. M3, the Delivery API and replay, is next. The Delivery API comes before replay because it replaces the test harness's direct reads of Herald's database: the end-to-end tests and the scenario then check Herald through its API, as a Tenant would. Later milestones are [GitHub milestones](https://github.com/madskrogh/herald/milestones), and the [`later`](https://github.com/madskrogh/herald/issues?q=is%3Aopen+label%3Alater) label marks planned work that isn't scheduled.

## Built with

Go, with the standard library first: `net/http` with Go 1.22+ routing patterns, `log/slog`, and `net/http/pprof`. Postgres through `pgx` and sqlc, with goose migrations embedded in the binaries. NATS through `nats.go`. gRPC, grpc-gateway and `buf` for the Tenant API. OpenTelemetry traces, through the Collector into ClickHouse. Tests use the standard `testing` package, `go-cmp` and testcontainers-go.

## Read more

- [Spec](spec/README.md): every rule Herald follows, with its reason and test vectors.
- [Architecture decisions](docs/adr/): the architecture, and the trade-offs behind it.
- [Testing](docs/testing.md): the kinds of tests, and where a new check belongs.
- [Glossary](GLOSSARY.md): the domain language used throughout the code.
- [Working with coding agents](docs/ai-workflow.md): how the lab is set up.
- [Case study](docs/case-study.md): what the workflow catches, and where.
