# Tenant API

How Tenants call Herald, and what it accepts. The operations, their fields and their REST paths are the protobuf package `herald.tenant.v1` in `api/` ([ADR 0004](../docs/adr/0004-protobuf-first-api.md)). Each error is given as its gRPC code and, after a slash, its REST status.

## Calls

### R-21 · implemented

Every Tenant API call MUST carry an API key as `authorization: Bearer <key>`, as an HTTP header over REST or as metadata over gRPC. A call without exactly one such key, or with a key Herald didn't issue, MUST fail with `UNAUTHENTICATED` / `401`.

Why: each API key acts for one Tenant, and the bearer scheme is the convention clients and gateways already support. Authentication schemes are case-insensitive (RFC 9110).

| `authorization` | Result |
|---|---|
| `Bearer <a valid key>` | the call goes ahead, for that key's Tenant |
| `bearer <a valid key>` | the call goes ahead, for that key's Tenant |
| (none) | `UNAUTHENTICATED` / `401` |
| `Bearer` | `UNAUTHENTICATED` / `401` |
| `Basic <a valid key>` | `UNAUTHENTICATED` / `401` |
| `Bearer <a key Herald didn't issue>` | `UNAUTHENTICATED` / `401` |
| two values, each `Bearer <a valid key>` | `UNAUTHENTICATED` / `401` |

### R-22 · implemented

Herald MUST NOT store an API key, only its SHA-256.

Why: a copy of the database, or a look at it, doesn't give anyone a key that calls the API.

| Key issued | Stored |
|---|---|
| any key | its SHA-256, and never the key |

### R-23 · implemented

When a call names an Application or Endpoint of another Tenant, Herald MUST answer as if it doesn't exist: `NOT_FOUND` / `404`, never `PERMISSION_DENIED`.

Why: an answer that differs would tell a Tenant which IDs exist under other Tenants.

| Call by Tenant A | Result |
|---|---|
| get an Application of Tenant B | `NOT_FOUND` / `404` |
| get an Application that doesn't exist | `NOT_FOUND` / `404` |
| create an Endpoint under an Application of Tenant B | `NOT_FOUND` / `404` |
| create a Message for an Application of Tenant B | `NOT_FOUND` / `404` |
| list the Endpoints of an Application of Tenant B | `NOT_FOUND` / `404` |

### R-24 · implemented

A list call MUST return at most `page_size` results, in a stable order, with a `next_page_token` that returns the next page, or an empty one on the last page. A `page_size` above `max-page-size` MUST be treated as `max-page-size`, and a negative one MUST fail with `INVALID_ARGUMENT` / `400`.

Why: lists are paginated from the start, since adding pagination later would silently truncate clients that don't page. The cap bounds the size of one response.

| Results | `page_size` | Pages returned |
|---|---|---|
| 3 | 2 | 2, then 1 with an empty `next_page_token` |
| 2 | 2 | 2, with an empty `next_page_token` |
| 150 | 500 | 100, then 50 |
| 3 | -1 | `INVALID_ARGUMENT` / `400` |

| `page_token` | Result |
|---|---|
| empty | the first page |
| the last page's `next_page_token` | the next page |
| `not-a-token` | `INVALID_ARGUMENT` / `400` |

### R-25 · implemented

When `page_size` is 0 or not set, Herald MAY choose the page size, up to `max-page-size`.

Why: any default serves a client that doesn't choose. Herald's is 50.

### R-26 · implemented

Every field MUST have one name across gRPC, REST bodies, REST query parameters, REST paths and the docs: the proto field name, in snake_case. Over REST, a body field the API doesn't know MUST fail the call with `INVALID_ARGUMENT` / `400`.

Why: a Tenant writes each field one way, whatever the protocol. Refusing an unknown field means a misspelled one fails, rather than being dropped: a misspelled `filter` would leave an Endpoint's filter empty, accepting every Event type ([R-2](delivery.md)).

| Over REST | Result |
|---|---|
| body `{"application_id": "…"}` | the field is read |
| body `{"filtr": ["invoice.*"]}` on create Endpoint | `INVALID_ARGUMENT` / `400` |
| query `?page_size=10` | the field is read |
| response field for the Application's ID | `application_id` |

### R-27 · implemented

A change to `herald.tenant.v1` MUST NOT break a client built against the version before it: not its wire encoding, its JSON field names, its gRPC method paths, its REST methods and paths, or the names in clients generated from the protos.

Why: Tenants depend on more than the wire encoding. A rename that keeps the encoding still breaks a JSON client or generated code.

| Change | Allowed |
|---|---|
| add a field | yes |
| add an operation | yes |
| rename a field | no |
| change a field's number or type | no |
| remove a field or an operation | no |
| change an operation's REST path or method | no |
| rename a package, service or file | no |

### R-28 · implemented

A request body over REST MUST be at most `max-request-body`. A larger body MUST fail with `413`, before the call is authenticated.

Why: the largest valid body is a create Message call, whose payload travels as a JSON string in which each byte may be escaped as six; a body larger than that is never valid. Checking before authentication bounds what a caller without a key can make Herald read.

| Body | API key | Result |
|---|---|---|
| a create Message call with a 256 KiB payload, every byte escaped as `\u00XX` | valid | the call goes ahead |
| 1600 KiB + 1 byte | valid | `413` |
| 1600 KiB + 1 byte | none | `413` |

## Applications

### R-29 · implemented

An Application's name MUST be at least one character other than whitespace, at most `application-name-bytes`, and without a NUL byte. Otherwise creating it MUST fail with `INVALID_ARGUMENT` / `400`.

Why: a blank name identifies nothing; the cap bounds what Herald stores for a name. Postgres text can't hold a NUL byte.

| Name | Result |
|---|---|
| `Globex` | created |
| 256 bytes | created |
| 257 bytes | `INVALID_ARGUMENT` / `400` |
| (empty) | `INVALID_ARGUMENT` / `400` |
| three spaces | `INVALID_ARGUMENT` / `400` |
| `a`, a NUL byte, `b` | `INVALID_ARGUMENT` / `400` |

## Endpoints

### R-30 · implemented

An Endpoint's URL MUST be an absolute `https` URL with a host, at most `endpoint-url-bytes`, with a port from 1 to 65535 if it has one, and without user information, a fragment, or unescaped whitespace. Otherwise creating the Endpoint MUST fail with `INVALID_ARGUMENT` / `400`.

Why: signing proves who sent a payload, but over plain `http` anyone on the path can read it, or answer `200` in the Endpoint's place. User information would be stored in plain, and a fragment is never sent, so the URL registered wouldn't be the URL requested. 2048 bytes is the length most clients and servers accept.

| URL | Result |
|---|---|
| `https://example.com/hook` | created |
| `HTTPS://example.com/hook` | created |
| `https://example.com:8443/hook?x=1` | created |
| `https://10.0.0.1/hook` | created ([R-41](security.md)) |
| `http://example.com/hook` | `INVALID_ARGUMENT` / `400` |
| `ftp://example.com/hook` | `INVALID_ARGUMENT` / `400` |
| `/hook` | `INVALID_ARGUMENT` / `400` |
| `https:///hook` | `INVALID_ARGUMENT` / `400` |
| `https://example.com:0/` | `INVALID_ARGUMENT` / `400` |
| `https://example.com:65536/` | `INVALID_ARGUMENT` / `400` |
| `https://user:pass@example.com/` | `INVALID_ARGUMENT` / `400` |
| `https://example.com/#top` | `INVALID_ARGUMENT` / `400` |
| `https://example.com/#` | `INVALID_ARGUMENT` / `400` |
| `https://example.com/a b` | `INVALID_ARGUMENT` / `400` |
| `https://example.com/` and 2028 more bytes, 2048 in all | created |
| `https://example.com/` and 2029 more bytes, 2049 in all | `INVALID_ARGUMENT` / `400` |

### R-31 · implemented

An Endpoint's filter MUST hold at most `max-filter-patterns` patterns, each matching `pattern` ([delivery.md](delivery.md)) and at most `filter-pattern-bytes` long. Otherwise creating the Endpoint MUST fail with `INVALID_ARGUMENT` / `400`.

Why: a filter is matched against every Message to its Application while the Tenant waits for the API to answer, and the caps bound that work. A pattern may be as long as an Event type, so a filter can name every Event type.

| Filter | Result |
|---|---|
| (empty) | created |
| `invoice.*`, `user.>` | created |
| 32 patterns | created |
| 33 patterns | `INVALID_ARGUMENT` / `400` |
| a 128-byte pattern | created |
| a 129-byte pattern | `INVALID_ARGUMENT` / `400` |
| `invoice.>.paid` | `INVALID_ARGUMENT` / `400` |
| `Invoice.paid` | `INVALID_ARGUMENT` / `400` |
| `invoice..paid` | `INVALID_ARGUMENT` / `400` |
| (one empty pattern) | `INVALID_ARGUMENT` / `400` |

### R-32 · implemented

An Application MUST NOT have more than `max-endpoints-per-application` Endpoints. Creating one more MUST fail with `FAILED_PRECONDITION` / `422`, even when several are created at once.

Why: a Message's Deliveries travel to the dispatchers together, in one message that must stay under the NATS maximum message size (1 MB by default), and each Delivery carries its Endpoint's URL and signing secrets. With a 256 KiB payload, about 350 Deliveries would fill 1 MB, so a Message to more Endpoints could never be handed off; 100 leaves a margin.

| Endpoints the Application has | Created | Result |
|---|---|---|
| 99 | 1 | created |
| 100 | 1 | `FAILED_PRECONDITION` / `422` |
| 98 | 5 at once | 2 created, 3 `FAILED_PRECONDITION` / `422` |

### R-33 · implemented

The response that creates an Endpoint MUST return its signing secret, new and its own: `whsec_` and the base64 of 24 random bytes. Every other response MUST leave it out.

Why: each Endpoint has its own secret, so a leaked one exposes one Endpoint ([ADR 0003](../docs/adr/0003-standard-webhooks-signing.md)). 24 bytes is the smallest key Standard Webhooks allows. Returning it once means a Tenant's later calls never carry it.

| Call | Response holds a signing secret |
|---|---|
| create Endpoint | yes, `whsec_` and 32 base64 characters |
| get Endpoint | no |
| list Endpoints | no |

### R-34 · implemented

The Tenant API MUST NOT offer a way to change an Endpoint's URL or filter after the Endpoint is created.

Why: each Delivery carries a copy of its Endpoint's details, so a change wouldn't reach the dispatchers ([ADR 0002](../docs/adr/0002-two-services-outbox-nats-handoff.md)). Making it possible is #119.

| Operation on an Endpoint | In `herald.tenant.v1` |
|---|---|
| create | yes |
| get | yes |
| list | yes |
| change its URL or filter | no |

## Messages

### R-35 · implemented

A Message's Event type MUST match `event-type` ([delivery.md](delivery.md)) and be at most `event-type-bytes` long. Otherwise creating the Message MUST fail with `INVALID_ARGUMENT` / `400`.

Why: Endpoints filter on Event types, so they follow the grammar filters are written in. Every Delivery the dispatchers receive carries its Message's Event type, and the cap bounds what each adds.

| Event type | Result |
|---|---|
| `invoice.paid` | created |
| `a` | created |
| `user_2.created_v1` | created |
| 128 bytes | created |
| 129 bytes | `INVALID_ARGUMENT` / `400` |
| (empty) | `INVALID_ARGUMENT` / `400` |
| `Invoice.paid` | `INVALID_ARGUMENT` / `400` |
| `invoice-paid` | `INVALID_ARGUMENT` / `400` |
| `invoice..paid` | `INVALID_ARGUMENT` / `400` |
| `.invoice` | `INVALID_ARGUMENT` / `400` |
| `invoice.*` | `INVALID_ARGUMENT` / `400` |

### R-36 · implemented

A Message's payload MUST be one JSON value, carried as text in a string field. A payload over `max-payload` MUST fail with `RESOURCE_EXHAUSTED` / `413`, and one that isn't a JSON value with `INVALID_ARGUMENT` / `400`.

Why: Herald delivers the payload's bytes unchanged ([R-5](delivery.md)), which a parsed `google.protobuf.Value` couldn't do. A webhook is a notification, not a way to move bulk data, so the cap leaves room for any notification.

| Payload | Result |
|---|---|
| `{"invoice": 42}` | created |
| `42` | created |
| a 256 KiB JSON value | created |
| a 256 KiB + 1 byte JSON value | `RESOURCE_EXHAUSTED` / `413` |
| (empty) | `INVALID_ARGUMENT` / `400` |
| `{"invoice": 42` | `INVALID_ARGUMENT` / `400` |
| `{} {}` | `INVALID_ARGUMENT` / `400` |

### R-37 · implemented

A create Message call MAY carry one Idempotency-Key, as the `Idempotency-Key` header over REST or the `idempotency-key` metadata over gRPC. It MUST be 1 to `idempotency-key-bytes` bytes of printable ASCII (`%x20-7E`), and a call with an invalid key or more than one MUST fail with `INVALID_ARGUMENT` / `400`.

Why: printable ASCII is what HTTP allows in the header. A key with other bytes can't be dropped silently, since the call would then go ahead without a key, and a retry would create a second Message. The cap keeps a key within what the database's unique index holds, with headroom.

| Idempotency-Key | Result |
|---|---|
| (none) | created |
| `order-42` | created |
| 256 bytes | created |
| 257 bytes | `INVALID_ARGUMENT` / `400` |
| (empty) | `INVALID_ARGUMENT` / `400` |
| `café` | `INVALID_ARGUMENT` / `400` |
| a tab | `INVALID_ARGUMENT` / `400` |
| two keys, `a` and `b` | `INVALID_ARGUMENT` / `400` |

### R-38 · implemented

When a create Message call carries an Idempotency-Key that one of the Tenant's Messages was created with less than `idempotency-window` before, and repeats that Message's request (the same Application, the same Event type, and the same payload, byte for byte), Herald MUST return that Message and create nothing.

Why: a Tenant that doesn't know whether its call succeeded can retry it safely. A client retries a failed request within hours, not days, so a day covers it. Payloads are compared as bytes, since that is how they are stored and delivered.

| Earlier call with key `k` | Later call with key `k` | Result |
|---|---|---|
| App A, `invoice.paid`, `{"n": 1}`, 1 h before | App A, `invoice.paid`, `{"n": 1}` | the earlier Message; nothing created |
| App A, `invoice.paid`, `{"n": 1}`, 23 h 59 m before | App A, `invoice.paid`, `{"n": 1}` | the earlier Message; nothing created |
| by Tenant B, App C, `invoice.paid`, `{"n": 1}`, 1 h before | by Tenant A, App A, `invoice.paid`, `{"n": 1}` | a new Message |

### R-39 · implemented

When a create Message call carries an Idempotency-Key that one of the Tenant's Messages was created with less than `idempotency-window` before, but differs from that Message's request, Herald MUST fail it with `FAILED_PRECONDITION` / `422` and create nothing.

Why: returning the earlier Message would drop the new one without telling the Tenant. Comparing bytes errs loudly: a client that writes the same JSON differently gets a `422`, never a Message silently dropped.

| Earlier call with key `k`, 1 h before | Later call with key `k` | Result |
|---|---|---|
| App A, `invoice.paid`, `{"n": 1}` | App B, `invoice.paid`, `{"n": 1}` | `FAILED_PRECONDITION` / `422` |
| App A, `invoice.paid`, `{"n": 1}` | App A, `invoice.voided`, `{"n": 1}` | `FAILED_PRECONDITION` / `422` |
| App A, `invoice.paid`, `{"n": 1}` | App A, `invoice.paid`, `{"n": 2}` | `FAILED_PRECONDITION` / `422` |
| App A, `invoice.paid`, `{"n": 1}` | App A, `invoice.paid`, `{"n":1}` | `FAILED_PRECONDITION` / `422` |

### R-40 · implemented

When a create Message call carries an Idempotency-Key whose Message was created `idempotency-window` or more before, Herald MUST treat it as a new key and create the Message. When several calls with the same new key arrive at once, Herald MUST create one Message, and answer each as R-38 and R-39 say.

Why: a key holds one Message for the window, and only one, however the calls race.

| Calls with key `k` | Result |
|---|---|
| one, 24 h after a Message was created with `k` | a new Message, which now holds `k` |
| one, 25 h after a Message was created with `k`, with a different request | a new Message, which now holds `k` |
| ten at once, with the same request, `k` unused | one Message, returned to all ten |
| ten at once, five with one request and five with another, `k` unused | one Message, returned to the five whose request it is; the other five `FAILED_PRECONDITION` / `422` |
