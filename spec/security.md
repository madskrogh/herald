# Security

How Attempts are signed, which addresses Herald connects to, and the setting that relaxes those rules for local development. Tenant authentication and the checks on Endpoint URLs are in [tenant-api.md](tenant-api.md).

## Signing

### R-42 · implemented

Every Attempt MUST be signed following Standard Webhooks: a `webhook-id` header ([R-6](delivery.md)); a `webhook-timestamp` header, the time the Attempt is made in Unix seconds; and a `webhook-signature` header holding, for each of the Endpoint's signing secrets, `v1,` and the base64 HMAC-SHA256 of `<webhook-id>.<webhook-timestamp>.<body>`, keyed with the base64-decoded part of the secret after `whsec_`.

Why: receivers verify with existing Standard Webhooks libraries in any language, so Herald ships no receiver library, and the timestamp lets them reject a replayed request ([ADR 0003](../docs/adr/0003-standard-webhooks-signing.md)).

| Signing secret | `webhook-id` | `webhook-timestamp` | Body | `webhook-signature` |
|---|---|---|---|---|
| `whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw` | `msg_p5jXN8AQM9LWM0D4loKWxJek` | `1614265330` | `{"test": 2432232314}` | `v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=` |
| `whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw` | `01a1f3d9-050c-740c-a24e-1c403ad913e6` | `1763556448` | `{"invoice": 43}` | `v1,YFcw+1Bn9LH4kHpgLnaf/jKIXPCCCAi/jXLFWM9qWY8=` |
| `whsec_oEhCmeiYNyR2FEdjWvHZhpDBLfoFIQKs` | `01a1f3d9-050c-740c-a24e-1c403ad913e6` | `1763556448` | `{"invoice": 43}` | `v1,mP8MUUMF4rvqEPacFroCmk7gXzNQmEh9owYpg/8Zm3g=` |

### R-43 · implemented

When an Endpoint has several signing secrets, `webhook-signature` MUST hold one signature for each, separated by single spaces.

Why: during a rotation, a receiver that knows only the old secret or only the new one can still verify; Standard Webhooks allows several signatures in one header ([ADR 0003](../docs/adr/0003-standard-webhooks-signing.md)).

| Signing secrets | `webhook-id` | `webhook-timestamp` | Body | `webhook-signature` |
|---|---|---|---|---|
| `whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw`, `whsec_oEhCmeiYNyR2FEdjWvHZhpDBLfoFIQKs` | `01a1f3d9-050c-740c-a24e-1c403ad913e6` | `1763556448` | `{"invoice": 43}` | `v1,YFcw+1Bn9LH4kHpgLnaf/jKIXPCCCAi/jXLFWM9qWY8= v1,mP8MUUMF4rvqEPacFroCmk7gXzNQmEh9owYpg/8Zm3g=` |

### R-44 · planned

When a Tenant rotates an Endpoint's signing secret, Herald MUST add a new secret, and the secrets the Endpoint had before MUST expire `secret-overlap` later.

Why: a Delivery already in flight may still carry only the old secret, and the overlap is longer than a Delivery's retry schedule, so its Attempts still verify ([ADR 0003](../docs/adr/0003-standard-webhooks-signing.md)).

### R-45 · planned

Herald MUST sign each Attempt with every signing secret of the Endpoint that hasn't expired, and MUST NOT sign with one that has.

Why: a receiver mid-rotation verifies with either secret (R-43), and an expired secret is one the Tenant has told its receiver to drop ([ADR 0003](../docs/adr/0003-standard-webhooks-signing.md)).

### R-46 · planned

An operator MUST be able to rotate the key that encrypts signing secrets without stopping Herald: secrets encrypted with an older key in the keyring still decrypt, and new ones are encrypted with the current key.

Why: an encryption key must be replaceable when it may have leaked, without an outage ([ADR 0003](../docs/adr/0003-standard-webhooks-signing.md)). Re-encrypting existing secrets under the new key is #121.

## Endpoint addresses

### R-41 · implemented

Creating an Endpoint MUST NOT depend on the address its host names or resolves to: the Tenant API doesn't refuse an Endpoint for one that isn't public.

Why: a public host name can resolve to a private address later (DNS rebinding), so a check when the Endpoint is created proves nothing. The check that counts is R-47's, on every connection.

| URL | Result |
|---|---|
| `https://10.0.0.1/hook` | created |
| `https://localhost/hook` | created |
| `https://example.com/hook` | created |

### R-47 · implemented

herald-dispatcher MUST NOT connect to an address that isn't globally reachable: loopback, private, link-local, unspecified, multicast, shared, reserved, documentation and benchmarking addresses, and IPv6 addresses outside `2000::/3`. It MUST check the address it actually dials, after DNS resolution, and for an IPv6 address that embeds an IPv4 one (IPv4-mapped, NAT64 or 6to4), the embedded address.

Why: an Endpoint URL is a Tenant's input. Without this, an Endpoint such as `https://169.254.169.254/` would make herald-dispatcher a proxy into Herald's own network.

| Address dialled | Connects |
|---|---|
| `8.8.8.8` | yes |
| `2606:4700:4700::1111` | yes |
| `64:ff9b::808:808` (NAT64 of `8.8.8.8`) | yes |
| `127.0.0.1` | no |
| `10.0.0.1` | no |
| `172.16.0.1` | no |
| `192.168.1.1` | no |
| `169.254.169.254` | no |
| `0.0.0.0` | no |
| `100.64.0.1` | no |
| `192.0.2.1` | no |
| `198.18.0.1` | no |
| `224.0.0.1` | no |
| `240.0.0.1` | no |
| `255.255.255.255` | no |
| `::1` | no |
| `::` | no |
| `fe80::1` | no |
| `fc00::1` | no |
| `ff02::1` | no |
| `2001:db8::1` | no |
| `::ffff:10.0.0.1` (IPv4-mapped) | no |
| `64:ff9b::a00:1` (NAT64 of `10.0.0.1`) | no |
| `2002:a00:1::1` (6to4 of `10.0.0.1`) | no |
| `example.com`, resolving to `10.0.0.1` | no |

### R-48 · implemented

When R-47 refuses a connection, the Attempt MUST fail, with an error that says the Endpoint's host resolved to an address Herald doesn't connect to and which kind, without naming the address. It is retried like any other failed Attempt.

Why: the Tenant can see why its Endpoint gets nothing, while the address, one inside Herald's network, stays hidden. A host can resolve to a public address again later, so the retry schedule still applies.

| Endpoint's host resolves to | Attempt | Error |
|---|---|---|
| `10.0.0.1` | failed, retried | `not connected: the Endpoint's host resolved to a private address, and Herald connects only to public addresses` |
| `127.0.0.1` | failed, retried | `not connected: the Endpoint's host resolved to a loopback address, and Herald connects only to public addresses` |

### R-49 · implemented

herald-dispatcher MUST connect to Endpoints directly, never through a proxy, whatever the proxy settings in its environment.

Why: a proxy would hide the address herald-dispatcher reaches, so R-47 couldn't check it.

| Environment | Connection goes to |
|---|---|
| `HTTPS_PROXY=http://proxy:3128` | the Endpoint's own address |
| no proxy set | the Endpoint's own address |

## Local development

### R-50 · implemented

When `HERALD_ALLOW_INSECURE_ENDPOINTS` is true, herald-api MUST accept `http` Endpoint URLs as well as `https` ones, and herald-dispatcher MUST connect to addresses R-47 refuses. When it is false or not set, both rules hold. A value that isn't a boolean MUST stop the service from starting.

Why: the local stack's and the tests' Endpoints are on private addresses without TLS. Both services read the setting the same way, so herald-api never accepts an Endpoint herald-dispatcher then refuses. Production never sets it.

| `HERALD_ALLOW_INSECURE_ENDPOINTS` | Create `http://example.com/hook` | Attempt to `10.0.0.1` |
|---|---|---|
| not set | `INVALID_ARGUMENT` / `400` | refused |
| `false` | `INVALID_ARGUMENT` / `400` | refused |
| `true` | created | made |
| `1` | created | made |
| `yes` | the service doesn't start | the service doesn't start |
