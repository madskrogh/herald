-- The spans of the trace a Message was handed off in, found by its handoff
-- span. Any span of the trace may arrive before the handoff's, and then none
-- is found yet.
SELECT TraceId, SpanId, ParentSpanId, SpanName, ServiceName, SpanAttributes, ResourceAttributes
FROM otel.otel_traces
WHERE TraceId IN (
    SELECT TraceId FROM otel.otel_traces
    WHERE SpanName = 'handoff' AND SpanAttributes['herald.message_id'] = {message_id:String}
)
ORDER BY SpanId
FORMAT JSONEachRow
