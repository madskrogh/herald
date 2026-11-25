# Try it

This walks one Message from the Tenant API to an Endpoint, makes the Endpoint fail so Herald retries, and then finds the Message's trace. Start the stack first with `make up` ([README](../README.md#try-it)). The commands are for bash or zsh.

## Send a Message

With the stack up, read the stack's ports from `.env`. Each checkout of the repo gets ports of its own; in the main checkout, the Tenant API is on 8080 and the Endpoint below on 9000.

```sh
. ./.env
API=localhost:$HERALD_API_HTTP_PORT
ENDPOINT_URL=http://host.docker.internal:$HERALD_DEMO_ENDPOINT_PORT/
```

Create a Tenant and an API key:

```sh
TENANT=$(make admin ARGS='tenant create --name Acme')
KEY=$(make admin ARGS="apikey create --tenant $TENANT")
```

Create an Application, and copy its `id` from the response:

```sh
curl -sS $API/v1/applications -H "Authorization: Bearer $KEY" -d '{"name": "Globex"}'
```

```json
{"application":{"id":"01a1f3d8-9882-75ee-9024-f3a2fb016df0", "name":"Globex", "created_at":"2026-11-19T12:47:00.738307Z"}}
```

```sh
APP=01a1f3d8-9882-75ee-9024-f3a2fb016df0
```

Register an Endpoint for it, and copy its `signing_secret` from the response:

```sh
curl -sS $API/v1/applications/$APP/endpoints -H "Authorization: Bearer $KEY" \
  -d '{"url": "'$ENDPOINT_URL'"}'
```

```json
{"endpoint":{"id":"01a1f3d8-aaa6-7bd0-9bcf-f4e6ac096503", "application_id":"01a1f3d8-9882-75ee-9024-f3a2fb016df0", "url":"http://host.docker.internal:9000/", "filter":[], "created_at":"2026-11-19T12:47:05.382887Z"}, "signing_secret":"whsec_oEhCmeiYNyR2FEdjWvHZhpDBLfoFIQKs"}
```

In a second terminal, serve that Endpoint. `make endpoint` prints each Attempt it receives and, given the signing secret, checks its signature. Like a real Endpoint, it rejects an Attempt whose signature doesn't verify, with 401, and Herald retries it:

```sh
make endpoint SECRET=whsec_oEhCmeiYNyR2FEdjWvHZhpDBLfoFIQKs
```

Back in the first terminal, send a Message:

```sh
curl -sS $API/v1/messages -H "Authorization: Bearer $KEY" \
  -d '{"application_id": "'$APP'", "event_type": "invoice.paid", "payload": "{\"invoice\": 42}"}'
```

The Attempt arrives at the Endpoint. Its `webhook-id` is the Message's ID:

```
Serving an Endpoint at http://host.docker.internal:9000/
Waiting for Attempts. Stop with Ctrl-C.

13:47:15.652 POST / webhook-id 01a1f3d8-d1dd-7e34-a179-d14e56907eb1: signature valid
{"invoice": 42}
```

## Watch it retry

Now make the Endpoint fail. Stop `make endpoint` with Ctrl-C, send another Message (`"invoice": 43`), and watch herald-dispatcher retry it:

```sh
docker compose logs -f herald-dispatcher
```

Each Attempt is a log line. With nothing listening, each fails without a status code:

```json
{"time":"2026-11-19T12:47:28.722643763Z","level":"INFO","msg":"attempt","service":"herald-dispatcher","delivery_id":"01a1f3d9-050d-715e-8e26-0775428d17d7","message_id":"01a1f3d9-050c-740c-a24e-1c403ad913e6","endpoint_id":"01a1f3d8-aaa6-7bd0-9bcf-f4e6ac096503","attempt":1,"status_code":0,"latency_ms":1,"state":"pending","attempt_error":"Post \"http://host.docker.internal:9000/\": dial tcp 192.168.65.254:9000: connect: connection refused","trace_id":"a55de8a61dd86f47e70640abec636640","span_id":"05906d6eaeecc84f"}
```

Within a minute, start `make endpoint` again, with the same secret. (Miss it, and the eighth Attempt fails too: the Delivery's state becomes `failed`, and Herald gives up on it. Send another Message to try again.) The next retry arrives with the same `webhook-id`, and the Delivery succeeds:

```
13:48:07.430 POST / webhook-id 01a1f3d9-050c-740c-a24e-1c403ad913e6: signature valid
{"invoice": 43}
```

Two settings make this local stack unlike a real deployment. Its retry schedule is compressed from about a day to about 80 seconds, so a Delivery is retried, and given up on, while you watch. And `HERALD_ALLOW_INSECURE_ENDPOINTS` lets it deliver over plain http to private addresses such as `host.docker.internal`, which a real deployment refuses.

## Where to look

Every Message is one trace, stored in ClickHouse: the Tenant API call, the handoff from herald-api to herald-dispatcher over NATS, the storing of its Deliveries, and each Attempt. Each span's parent is in `ParentSpanId`. To see the second Message's trace, put its ID in this query:

```sh
docker compose exec clickhouse clickhouse-client --user herald --password herald --query "
  SELECT Timestamp, ServiceName, SpanName, SpanId, ParentSpanId,
         SpanAttributes['herald.attempt'] AS attempt,
         SpanAttributes['herald.status_code'] AS status,
         SpanAttributes['herald.outcome'] AS outcome
  FROM otel.otel_traces
  WHERE TraceId IN (SELECT TraceId FROM otel.otel_traces
                    WHERE SpanAttributes['herald.message_id'] = '01a1f3d9-050c-740c-a24e-1c403ad913e6')
  ORDER BY Timestamp
  FORMAT PrettyCompactMonoBlock"
```

```
    ┌─────────────────────Timestamp─┬─ServiceName───────┬─SpanName─────────┬─SpanId───────────┬─ParentSpanId─────┬─attempt─┬─status─┬─outcome────┐
 1. │ 2026-11-19 12:47:28.522267305 │ herald-api        │ tenant_api.call  │ 1061bc1ecfab2910 │                  │         │        │ OK         │
 2. │ 2026-11-19 12:47:28.589176513 │ herald-api        │ handoff          │ 7a1f0d17616305dd │ 1061bc1ecfab2910 │         │        │ handed_off │
 3. │ 2026-11-19 12:47:28.589752263 │ herald-dispatcher │ store_deliveries │ afcf45e6d798da90 │ 7a1f0d17616305dd │         │        │ stored     │
 4. │ 2026-11-19 12:47:28.720205972 │ herald-dispatcher │ attempt          │ 05906d6eaeecc84f │ afcf45e6d798da90 │ 1       │ 0      │ pending    │
 5. │ 2026-11-19 12:47:29.223982139 │ herald-dispatcher │ attempt          │ 61b78775b2ee468b │ afcf45e6d798da90 │ 2       │ 0      │ pending    │
 6. │ 2026-11-19 12:47:29.730473833 │ herald-dispatcher │ attempt          │ e3cfce98dd09b081 │ afcf45e6d798da90 │ 3       │ 0      │ pending    │
 7. │ 2026-11-19 12:47:30.737931250 │ herald-dispatcher │ attempt          │ f1f45cc733fbc885 │ afcf45e6d798da90 │ 4       │ 0      │ pending    │
 8. │ 2026-11-19 12:47:34.755330669 │ herald-dispatcher │ attempt          │ b00e338bdf71cb3e │ afcf45e6d798da90 │ 5       │ 0      │ pending    │
 9. │ 2026-11-19 12:47:46.806718799 │ herald-dispatcher │ attempt          │ aed5f3bb91837c72 │ afcf45e6d798da90 │ 6       │ 0      │ pending    │
10. │ 2026-11-19 12:48:07.422528712 │ herald-dispatcher │ attempt          │ d8a354c3a128836c │ afcf45e6d798da90 │ 7       │ 200    │ succeeded  │
    └───────────────────────────────┴───────────────────┴──────────────────┴──────────────────┴──────────────────┴─────────┴────────┴────────────┘
```

The services' logs are JSON lines, read with `docker compose logs herald-api herald-dispatcher`. Each line about a Message carries the `trace_id` of its trace. The Delivery API, which will show a Message's Deliveries and Attempts without going to either, comes in M3.
