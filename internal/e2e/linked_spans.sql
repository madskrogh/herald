-- The spans that link to the span with the given ID, such as the Batch that
-- holds a Message's handoff.
SELECT TraceId, SpanId, ParentSpanId, SpanName, ServiceName, SpanAttributes, ResourceAttributes
FROM otel.otel_traces
WHERE has(Links.SpanId, {span_id:String})
ORDER BY SpanId
FORMAT JSONEachRow
