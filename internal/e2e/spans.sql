-- The spans of one trace, as the Collector's ClickHouse exporter stores them.
SELECT TraceId, SpanId, ParentSpanId, SpanName, ServiceName,
       toString(toUnixTimestamp64Nano(Timestamp)) AS StartNs,
       toString(toUnixTimestamp64Nano(Timestamp) + Duration) AS EndNs,
       StatusCode, SpanAttributes, ResourceAttributes,
       Links.TraceId AS LinkTraceIds, Links.SpanId AS LinkSpanIds
FROM otel.otel_traces
WHERE TraceId = {trace_id:String}
ORDER BY SpanId
FORMAT JSONEachRow
