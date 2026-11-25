# Herald

Herald reliably delivers webhooks on behalf of SaaS products to the HTTP endpoints their customers register.

## Language

### Parties

**Tenant**:
An organization that uses Herald to send webhooks to its own customers, e.g. a SaaS company.
_Avoid_: Customer, account, organization, client

**Application**:
One of a Tenant's customers, as seen by Herald: the owner of a set of Endpoints that should receive that customer's webhooks.
_Avoid_: Consumer, subscriber, customer, app

**Endpoint**:
A URL registered under an Application to which Herald delivers webhooks. It may restrict which event types it accepts.
_Avoid_: Webhook URL, target, destination, receiver

**Signing secret**:
The secret Herald uses to sign webhooks to an Endpoint, so the receiver can verify they came from Herald. Not to be confused with a Tenant's API key, which authenticates the Tenant to Herald.
_Avoid_: Webhook secret, endpoint key, API key

### What flows through

**Message**:
A single notification a Tenant submits for one Application, consisting of an event type and a payload.
_Avoid_: Event, webhook, notification, payload

**Event type**:
A dot-separated name classifying a Message, such as `invoice.paid`, which Endpoints filter on.
_Avoid_: Topic, subject, kind, event

**Delivery**:
One Message on its way to one Endpoint. A Message to an Application with two matching Endpoints has two Deliveries.
_Avoid_: Dispatch, send, job

**Outbox**:
The Messages Herald has accepted whose Deliveries haven't been handed off yet, other than those that are Parked.
_Avoid_: Queue, pending messages

**Parked**:
Taken out of the Outbox without being handed off, because the dispatcher rejects the Message every time, alone as well as in a Batch. A parked Message no longer blocks the Messages behind it. It keeps the time it was parked and the dispatcher's error, and returns to the Outbox when both are cleared.
_Avoid_: Dead-lettered, poisoned, quarantined, failed

**Handoff**:
Passing a Message's Deliveries from the side of Herald that accepts Messages to the side that makes Attempts. A Message is handed off once the other side has stored all its Deliveries.
_Avoid_: Dispatch, publish, forward, sync

**Batch**:
A set of Messages handed off together, each with all of its Deliveries. A Batch is handed off whole or not at all.
_Avoid_: Chunk, bundle, page

**Attempt**:
One HTTP request made for a Delivery. A Delivery has one or more Attempts.
_Avoid_: Try, call, request

**Replay**:
Starting a fresh retry schedule for an existing Delivery, whether it succeeded or failed, with the same webhook ID.
_Avoid_: Resend, retry, redeliver

**Retention**:
How long Herald keeps a Message, with its Deliveries and their Attempts, counted from when the Message was accepted. After it, all of them are deleted together. A replay doesn't extend it.
_Avoid_: TTL, expiry, cleanup, purge

### The spec

**Spec**:
The numbered requirements in `spec/`, which state every decision in Herald that someone at its boundaries can notice, and which Herald's code is derived from. Not an issue, a design doc or the code.
_Avoid_: Specification document, design doc, RFC, requirements doc

**Requirement**:
One rule in the spec, with a stable ID (`R-4`), a status (implemented, planned or withdrawn), and the reason for it.
_Avoid_: Clause, spec item, acceptance criterion

**Test vector**:
A table of cases for one requirement (given these inputs, Herald gives these results) that every build's tests must pass. Tests are written from it or read it, and add cases of their own. Usually called a vector.
_Avoid_: Test case, fixture, example, golden file

### Testing

**Scenario invariant**:
A rule about Herald's behaviour that holds on any build, checked after every run of the scenario. Unlike the behaviour comparison, it has no baseline: a violation is a bug, never a change to approve.
_Avoid_: Telemetry invariant, scenario assertion
