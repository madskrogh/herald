-- The spans of one trace, as the Collector's ClickHouse exporter stores them.
SELECT TraceId, SpanId, SpanName, ServiceName, SpanAttributes
FROM otel.otel_traces
WHERE TraceId = {trace_id:String}
ORDER BY SpanId
FORMAT JSONEachRow
