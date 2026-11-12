# Herald

Herald delivers webhooks on behalf of SaaS products: signed, retried, and observable. A Tenant submits a Message once. Herald works out which of the Application's Endpoints want it, signs every Attempt with [Standard Webhooks](https://www.standardwebhooks.com/), and retries failures for about 24 hours.

## Architecture

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

Two services, each with its own schema. herald-api accepts a Message and writes it together with its Deliveries in one transaction. A relay hands them over to herald-dispatcher in batches over NATS, and marks them handed off only once the dispatcher has committed them. Delivery is at-least-once, and every hop is safe to repeat. A request repeated with the same `Idempotency-Key` returns the original Message, a resent batch inserts nothing new, and every Attempt of a Delivery carries the same `webhook-id`, so Endpoints can drop repeats.

## Status

Under construction. Milestone M1, the core delivery path, is in progress. The milestones are listed in the [design doc](docs/design.md#milestones).

## Read more

- [Design](docs/design.md): the product spec, architecture and delivery semantics.
- [Architecture decisions](docs/adr/): the trade-offs behind the design.
- [Glossary](GLOSSARY.md): the domain language used throughout the code.
- [Working with coding agents](docs/ai-workflow.md): Herald is also a lab for agent-assisted development. This is how it's set up.
