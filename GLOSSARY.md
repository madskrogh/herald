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

**Attempt**:
One HTTP request made for a Delivery. A Delivery has one or more Attempts.
_Avoid_: Try, call, request

**Replay**:
Starting a fresh retry schedule for an existing Delivery, whether it succeeded or failed, with the same webhook ID.
_Avoid_: Resend, retry, redeliver
