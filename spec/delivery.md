# Delivery

Which Endpoints a Message reaches, how each Attempt is made, when Herald retries, and how one Endpoint is kept from affecting the others. Terms are defined in [GLOSSARY.md](../GLOSSARY.md), and values such as `attempt-timeout` in [limits.md](limits.md).

## Event types and filters

An Event type is dot-separated tokens. An Endpoint's filter is a list of patterns: Event types in which any token may be `*`, and whose last token may be `>`.

```abnf
event-type    = token *("." token)
token         = 1*(%x61-7A / DIGIT / "_")   ; a-z, 0-9 and _
pattern       = *(pattern-token ".") last-token
pattern-token = token / "*"
last-token    = pattern-token / ">"
```

The Tenant API refuses an Event type or a filter that doesn't follow these rules ([tenant-api.md](tenant-api.md)).

### R-1 · implemented

When Herald accepts a Message, it MUST create exactly one Delivery for each Endpoint of the Message's Application whose filter matches the Message's Event type, and no Delivery for any other Endpoint.

Why: a Delivery is one Message on its way to one Endpoint, and the filter is how an Endpoint chooses which Event types it receives.

| Endpoints and their filters | Event type | Deliveries to |
|---|---|---|
| A: none; B: `invoice.*`; C: `user.>` | `invoice.paid` | A, B |
| A: `user.>` | `invoice.paid` | none |
| A: `invoice.paid`; B: `invoice.paid` | `invoice.paid` | A, B |
| (no Endpoints) | `invoice.paid` | none |

### R-2 · implemented

A filter with no patterns MUST match every Event type.

Why: an Endpoint that names no Event types wants all of them, so registering one needs no knowledge of the Event types its Tenant sends.

| Filter | Event type | Matches |
|---|---|---|
| (empty) | `invoice.paid` | yes |
| (empty) | `a` | yes |
| (empty) | `a.b.c.d` | yes |

### R-3 · implemented

A filter with patterns MUST match an Event type when at least one of its patterns matches it, and only then.

Why: an Endpoint can take several unrelated Event types without a pattern that also takes ones it doesn't want.

| Filter | Event type | Matches |
|---|---|---|
| `invoice.paid`, `user.created` | `user.created` | yes |
| `invoice.paid`, `user.created` | `user.deleted` | no |
| `invoice.*`, `invoice.paid` | `invoice.paid` | yes |

### R-4 · implemented

A pattern MUST match an Event type token by token: a token matches the same token, `*` matches any one token, and a final `>` matches one or more tokens. Without a `>`, a pattern MUST NOT match an Event type with a different number of tokens.

Why: wildcards let an Endpoint take a family of Event types, such as every one that starts with `invoice`, without listing each one, including ones the Tenant adds later. The syntax is NATS's, an existing convention for dot-separated names.

| Pattern | Event type | Matches |
|---|---|---|
| `invoice.paid` | `invoice.paid` | yes |
| `invoice.paid` | `invoice.paid.late` | no |
| `invoice.paid` | `invoice` | no |
| `invoice.*` | `invoice.paid` | yes |
| `invoice.*` | `invoice` | no |
| `invoice.*` | `invoice.paid.late` | no |
| `*.paid` | `invoice.paid` | yes |
| `*` | `invoice.paid` | no |
| `invoice.>` | `invoice.paid` | yes |
| `invoice.>` | `invoice.paid.late` | yes |
| `invoice.>` | `invoice` | no |
| `>` | `invoice` | yes |
| `*.>` | `invoice.paid.late` | yes |

## Attempts

### R-5 · implemented

An Attempt MUST be an HTTP `POST` to the Endpoint's URL, with the Message's payload as its body, byte for byte as the Tenant submitted it, `Content-Type: application/json` and `User-Agent: Herald`.

Why: the Endpoint sees exactly what the Tenant sent. Parsing the payload and writing it again would change it: numbers would become 64-bit floats, so integers above 2^53 would silently change. The `User-Agent` names the sender in the Endpoint's own logs.

| Payload submitted | Body received |
|---|---|
| `{"n": 9007199254740993}` | `{"n": 9007199254740993}` |
| `{ "a":1 , "b" : [ ] }` | `{ "a":1 , "b" : [ ] }` |
| `"text"` | `"text"` |
| `[1.0, 1e2]` | `[1.0, 1e2]` |

### R-6 · implemented

Every Attempt for a Message MUST carry the Message's ID as its `webhook-id` header: the same on every Attempt of a Delivery, and on every Delivery of the Message.

Why: delivery is at-least-once, so an Endpoint may receive a Message more than once, and a stable ID is what it drops the repeats by ([ADR 0001](../docs/adr/0001-at-least-once-delivery.md)).

| Attempt | `webhook-id` |
|---|---|
| Attempt 1 of the Delivery to Endpoint A | the Message's ID |
| Attempt 2 of the Delivery to Endpoint A, after Attempt 1 failed | the Message's ID |
| Attempt 1 of the Delivery to Endpoint B | the Message's ID |

### R-7 · implemented

An Attempt MUST succeed when the Endpoint answers with a `2xx` status within `attempt-timeout` of the Attempt's start, and fail otherwise: on any other status, on a timeout, and on a connection or TLS error.

Why: a `2xx` is the Endpoint's acknowledgement that it has the Message ([ADR 0001](../docs/adr/0001-at-least-once-delivery.md)). Anything else may mean it doesn't, so Herald retries. An Endpoint that doesn't answer in time is treated as one that failed, so Herald never waits on it indefinitely.

| Response | Within `attempt-timeout` | Attempt |
|---|---|---|
| `200` | yes | succeeded |
| `204` | yes | succeeded |
| `299` | yes | succeeded |
| `302` | yes | failed |
| `404` | yes | failed |
| `500` | yes | failed |
| `200` | no | failed |
| connection refused | n/a | failed |
| connection reset | n/a | failed |
| TLS handshake error | n/a | failed |

### R-8 · implemented

Herald MUST NOT follow a redirect: it makes no request to the `Location` a `3xx` response names.

Why: following one would let an Endpoint send Attempts on to another URL, including a plain `http` one that anyone on the path could read ([R-30](tenant-api.md)).

| Response to the Attempt | Requests made | Attempt |
|---|---|---|
| `301` to `https://elsewhere.example/` | 1 | failed |
| `307` to `http://example.com/hook` | 1 | failed |

### R-9 · implemented

When an Attempt succeeds, Herald MUST set its Delivery's state to `succeeded`, and MUST NOT make another Attempt for that Delivery.

Why: a `2xx` is the Endpoint's acknowledgement ([ADR 0001](../docs/adr/0001-at-least-once-delivery.md)).

| Responses | Attempts made | State |
|---|---|---|
| `200` | 1 | `succeeded` |
| `500`, `503`, `200` | 3 | `succeeded` |

### R-10 · implemented

A Delivery's first Attempt MUST be due as soon as the Delivery is stored.

Why: most Endpoints are up, and a Tenant expects its Message to reach them promptly.

| Event | Attempt 1 due |
|---|---|
| The Delivery is stored | at once |

### R-11 · implemented

When Attempt n of a Delivery fails and n is less than `max-attempts`, Herald MUST make Attempt n+1 once a delay has passed since Attempt n ended. That delay MUST be the n-th of `retry-delays`, varied by at most `retry-jitter` either way.

Why: a failed Attempt may succeed once the Endpoint recovers, so Herald retries on a schedule rather than at once or never ([ADR 0001](../docs/adr/0001-at-least-once-delivery.md)). `retry-delays` and `retry-jitter` give the reasons for their values.

| Failed Attempt | Next Attempt | Delay | Shortest | Longest |
|---|---|---|---|---|
| 1 | 2 | 5 s | 4.5 s | 5.5 s |
| 2 | 3 | 1 m | 54 s | 66 s |
| 3 | 4 | 10 m | 9 m | 11 m |
| 4 | 5 | 1 h | 54 m | 66 m |
| 5 | 6 | 3 h | 2 h 42 m | 3 h 18 m |
| 6 | 7 | 6 h | 5 h 24 m | 6 h 36 m |
| 7 | 8 | 12 h | 10 h 48 m | 13 h 12 m |

### R-12 · implemented

When Attempt `max-attempts` of a Delivery fails, Herald MUST set the Delivery's state to `failed`, and MUST NOT make another Attempt for it.

Why: once the retry schedule has run out, the Endpoint is unlikely to recover on its own, and the Tenant needs a final answer.

| Responses | Attempts made | State |
|---|---|---|
| `500` eight times | 8 | `failed` |
| `500` seven times, then `200` | 8 | `succeeded` |
| connection refused eight times | 8 | `failed` |

### R-13 · implemented

A Delivery's state MUST be `pending` from when it is stored until it becomes `succeeded` or `failed`, and MUST NOT change after that.

Why: a Tenant (through the Delivery API, M3) and an operator read one of three states, and a final state is final.

| After | State |
|---|---|
| the Delivery is stored | `pending` |
| Attempt 1 fails | `pending` |
| an Attempt succeeds | `succeeded` |
| Attempt 8 fails | `failed` |

### R-14 · planned

Unlike R-13, when a Tenant replays a Delivery, whether it succeeded or failed, Herald MUST start a fresh retry schedule for it, with the same `webhook-id`.

Why: a Tenant can send a Message to an Endpoint again once the Endpoint is fixed, or when it lost one. The same `webhook-id` lets the Endpoint drop it if it already has it ([ADR 0001](../docs/adr/0001-at-least-once-delivery.md)).

### R-15 · planned

When a Delivery is replayed, Herald MUST sign its Attempts with the Endpoint's current signing secrets, not those the Delivery was first signed with.

Why: a replay may come after a rotation has expired the secrets the Delivery was stored with ([ADR 0003](../docs/adr/0003-standard-webhooks-signing.md)).

### R-16 · implemented

Herald MUST keep every Attempt it records for a Delivery, with its number (counted from 1), its status code (`0` when there was no response), its latency, the start of the response body, up to `response-body-kept`, and, when there was no response, the error that prevented one.

Why: the Attempt history is how a Tenant (through the Delivery API, M3) and a developer find out why a Delivery failed. With a response, its status and body say why; without one, only the error does.

| Endpoint's answer | Status code kept | Body kept | Error kept |
|---|---|---|---|
| `500` with a 10 KiB body | `500` | its first 4 KiB | none |
| `200` with an empty body | `200` | empty | none |
| connection refused | `0` | none | says the connection was refused |

### R-17 · implemented

Unlike R-11, when Herald can't tell whether an Attempt was made and recorded, because a dispatcher stopped or lost its database during it, it MAY make that Attempt again, before the retry schedule allows.

Why: delivery is at-least-once ([ADR 0001](../docs/adr/0001-at-least-once-delivery.md)). Repeating an Attempt that may have happened is how Herald avoids losing one that didn't.

### R-18 · planned

Herald MUST delete each Delivery, with its Attempts, once it is older than `retention`.

Why: storage grows with every Delivery, so history has to end; `retention` says when.

## Isolation

### R-19 · implemented

Herald MUST NOT have more than `max-in-flight-per-endpoint` Attempts in flight to one Endpoint at any moment, across all of its dispatchers.

Why: without a cap, a slow Endpoint with many Deliveries due would draw Attempts until it held every worker, and Herald would pile load on an Endpoint that is already struggling.

| Endpoint | Deliveries due at once | Most Attempts in flight |
|---|---|---|
| answers after 10 s | 20 | 4 |
| answers at once | 20 | at most 4 |
| answers after 10 s, with two dispatchers | 20 | 4 |

### R-20 · implemented

While an Endpoint is slow to answer, Attempts to other Endpoints MUST NOT wait for its Attempts.

Why: one misbehaving Endpoint must not affect deliveries to anyone else.

| Endpoint A | Endpoint B | Attempt to B |
|---|---|---|
| 10 Deliveries due, each answered after `attempt-timeout` | 1 Delivery due, answered at once | made while A's Attempts are in flight |

### R-61 · implemented

Herald MAY make Attempts to an Endpoint in any order, whatever order their Messages were accepted in.

Why: delivering in order would let one failing Message hold back every Message behind it to the same Endpoint. Ordered delivery is #123.

## Parked Messages

### R-59 · implemented

Unlike R-1, Herald MUST park a Message, and make no Delivery for it, when a dispatcher refuses the Message on its own while it stores other Messages handed off with it, or when the Message is too large to hand off at all. A parked Message leaves the Outbox, so the Messages behind it are handed off, and it keeps the time it was parked and the dispatcher's error.

Why: a Message no dispatcher will ever store would otherwise block every Message behind it in the Outbox. Refusing it alone, while others are stored, shows the Message is at fault rather than the dispatcher; when a dispatcher stores nothing, it may be failing as a whole, and every Message waits for it to recover. The time and error let an operator find a parked Message and why it was parked.

| Messages handed off together | Dispatcher | Result |
|---|---|---|
| M1, M2, M3 | refuses M2, alone or with others; stores the rest | M1 and M3 handed off; M2 parked, with the dispatcher's error |
| M1, M2 | refuses everything | both stay in the Outbox, and are handed off once the dispatcher stores them |
| M1, larger than any handoff may be | not asked | M1 parked, with an error saying it is too large |

### R-60 · planned

When an operator clears a parked Message's parking time and error, the Message MUST return to the Outbox, and be handed off like any other.

Why: once the cause is fixed, the Message can still be delivered; parking holds it aside, it doesn't drop it.
