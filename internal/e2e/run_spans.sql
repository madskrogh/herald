-- The spans of a Tenant's run: every span of each trace that has a span of
-- the Tenant's, and the Batches that link to the Tenant's handoffs. A Batch
-- is a trace of its own, and may hold other Tenants' Messages too.
SELECT TraceId, SpanId, ParentSpanId, SpanName, ServiceName,
       toString(toUnixTimestamp64Nano(Timestamp)) AS StartNs,
       toString(toUnixTimestamp64Nano(Timestamp) + Duration) AS EndNs,
       StatusCode, SpanAttributes, ResourceAttributes,
       Links.TraceId AS LinkTraceIds, Links.SpanId AS LinkSpanIds
FROM otel.otel_traces
WHERE TraceId IN (
    SELECT TraceId FROM otel.otel_traces
    WHERE SpanAttributes['herald.tenant_id'] = {tenant_id:String}
)
OR (SpanName = 'handoff.batch' AND hasAny(Links.SpanId, (
    SELECT groupArray(SpanId) FROM otel.otel_traces
    WHERE SpanName = 'handoff' AND SpanAttributes['herald.tenant_id'] = {tenant_id:String}
)))
ORDER BY SpanId
FORMAT JSONEachRow
