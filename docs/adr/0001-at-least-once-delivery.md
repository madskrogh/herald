# At-least-once delivery

Herald guarantees at-least-once delivery, not exactly-once. A receiver's acknowledgement (its `2xx` response) can be lost after the receiver has already processed a webhook, and Herald cannot tell that apart from a webhook that was never processed. The only safe choice is to retry, which means receivers may see duplicates. Every webhook therefore carries a stable ID and a signature, and receivers are expected to drop duplicates by that ID. This is how established webhook providers behave (Stripe, GitHub, Svix).

## Considered Options

- **Exactly-once delivery with receiver acknowledgements**: rejected. Any acknowledgement can itself be lost (the Two Generals problem), so this still collapses to at-least-once or at-most-once.
- **At-most-once (no retries)**: rejected. It silently loses webhooks whenever an endpoint or the network has a transient failure.

## Consequences

- Receivers must be idempotent. The docs say so explicitly. Herald does not ship a receiver library.
- Herald never needs distributed transactions with receivers. Retries can be aggressive without risking correctness on Herald's side.
