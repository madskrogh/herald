-- The spans that link to the span with the given ID, such as the Batch that
-- holds a Message's handoff.
SELECT TraceId, SpanId, ParentSpanId, SpanName, ServiceName,
       toString(toUnixTimestamp64Nano(Timestamp)) AS StartNs,
       toString(toUnixTimestamp64Nano(Timestamp) + Duration) AS EndNs,
       StatusCode, SpanAttributes, ResourceAttributes,
       Links.TraceId AS LinkTraceIds, Links.SpanId AS LinkSpanIds
FROM otel.otel_traces
WHERE has(Links.SpanId, {span_id:String})
ORDER BY SpanId
FORMAT JSONEachRow
