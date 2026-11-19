package e2e

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Span is one span, as sent to the Collector and as read back from
// ClickHouse.
type Span struct {
	// TraceID, SpanID and ParentSpanID are in hex, as OTLP/JSON and
	// ClickHouse write them.
	TraceID string
	SpanID  string
	// ParentSpanID is empty for a root span. SendSpan sends only roots.
	ParentSpanID string
	Name         string
	ServiceName  string
	Attributes   map[string]string
	// ResourceAttributes are those of the service that sent the span,
	// service.name among them. SendSpan sends only service.name, from
	// ServiceName.
	ResourceAttributes map[string]string
}

// SendSpan sends one span to the stack's Collector over OTLP/HTTP, encoded
// as JSON.
func (s *Stack) SendSpan(ctx context.Context, span Span) error {
	attrs := make([]otlpKeyValue, 0, len(span.Attributes))
	for k, v := range span.Attributes {
		attrs = append(attrs, otlpString(k, v))
	}
	now := time.Now()
	req := otlpRequest{ResourceSpans: []otlpResourceSpans{{
		Resource: otlpResource{Attributes: []otlpKeyValue{otlpString("service.name", span.ServiceName)}},
		ScopeSpans: []otlpScopeSpans{{
			Scope: otlpScope{Name: "github.com/madskrogh/herald/internal/e2e"},
			Spans: []otlpSpan{{
				TraceID:           span.TraceID,
				SpanID:            span.SpanID,
				Name:              span.Name,
				Kind:              otlpSpanKindInternal,
				StartTimeUnixNano: strconv.FormatInt(now.UnixNano(), 10),
				EndTimeUnixNano:   strconv.FormatInt(now.Add(time.Millisecond).UnixNano(), 10),
				Attributes:        attrs,
			}},
		}},
	}}}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("send span: encode: %w", err)
	}
	header := http.Header{"Content-Type": {"application/json"}}
	data, err := s.post(ctx, s.otlpURL+"/v1/traces", header, body)
	if err != nil {
		return fmt.Errorf("send span: %w", err)
	}
	// The Collector answers 200 OK even when it rejects the span, and says
	// so in the response.
	var resp struct {
		PartialSuccess struct {
			RejectedSpans string `json:"rejectedSpans"`
			ErrorMessage  string `json:"errorMessage"`
		} `json:"partialSuccess"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("send span: decode response: %w", err)
	}
	if p := resp.PartialSuccess; p.RejectedSpans != "" && p.RejectedSpans != "0" {
		return fmt.Errorf("send span: Collector rejected it: %s", p.ErrorMessage)
	}
	return nil
}

// Queries for spans in ClickHouse, each selecting the same columns, ordered
// by span ID.
var (
	// spansQuery selects one trace's spans. It takes the query parameter
	// trace_id.
	//
	//go:embed spans.sql
	spansQuery string
	// messageTraceQuery selects the spans of the trace a Message was handed
	// off in. It takes the query parameter message_id.
	//
	//go:embed message_trace.sql
	messageTraceQuery string
	// linkedSpansQuery selects the spans that link to a span. It takes the
	// query parameter span_id.
	//
	//go:embed linked_spans.sql
	linkedSpansQuery string
)

// WaitSpans waits until ClickHouse holds at least one span of the trace with
// the given ID, and returns the trace's spans stored so far, ordered by span
// ID. The Collector batches spans before it exports them, so they arrive a
// moment after they are sent, and the spans of a larger trace may arrive in
// more than one batch.
func (s *Stack) WaitSpans(ctx context.Context, traceID string) ([]Span, error) {
	spans, err := s.waitSpans(ctx, spansQuery, "trace_id", traceID, 1)
	if err != nil {
		return nil, fmt.Errorf("trace %s: waiting for its spans: %w", traceID, err)
	}
	return spans, nil
}

// WaitMessageTrace waits until ClickHouse holds at least n spans of the
// trace the Message with the given ID was handed off in, and returns them,
// ordered by span ID. Spans that arrive later aren't returned.
func (s *Stack) WaitMessageTrace(ctx context.Context, messageID string, n int) ([]Span, error) {
	spans, err := s.waitSpans(ctx, messageTraceQuery, "message_id", messageID, n)
	if err != nil {
		return spans, fmt.Errorf("message %s: waiting for %d spans of its trace, have %d: %w", messageID, n, len(spans), err)
	}
	return spans, nil
}

// WaitBatchesLinkedTo waits until ClickHouse holds at least one span that
// links to the span with the given ID, and returns those stored so far,
// ordered by span ID.
func (s *Stack) WaitBatchesLinkedTo(ctx context.Context, spanID string) ([]Span, error) {
	spans, err := s.waitSpans(ctx, linkedSpansQuery, "span_id", spanID, 1)
	if err != nil {
		return nil, fmt.Errorf("span %s: waiting for spans linked to it: %w", spanID, err)
	}
	return spans, nil
}

// waitSpans runs query, with the query parameter param set to value, until
// it returns at least n spans, and returns the last spans it returned.
func (s *Stack) waitSpans(ctx context.Context, query, param, value string, n int) ([]Span, error) {
	var spans []Span
	err := poll(ctx, func() (bool, error) {
		var err error
		spans, err = s.querySpans(ctx, query, param, value)
		return len(spans) >= n, err
	})
	return spans, err
}

// querySpans runs query in ClickHouse, with the query parameter param set to
// value, and returns the spans it selects.
func (s *Stack) querySpans(ctx context.Context, query, param, value string) ([]Span, error) {
	// wait_end_of_query makes ClickHouse report an error that happens while
	// it streams rows as an error status, not as text after the rows.
	params := url.Values{"param_" + param: {value}, "wait_end_of_query": {"1"}}
	target := s.clickHouseURL + "/?" + params.Encode()
	header := http.Header{"Content-Type": {"text/plain"}}
	data, err := s.post(ctx, target, header, []byte(query))
	if err != nil {
		return nil, fmt.Errorf("query spans by %s %s: %w", param, value, err)
	}
	var spans []Span
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var row struct {
			TraceID            string            `json:"TraceId"`
			SpanID             string            `json:"SpanId"`
			ParentSpanID       string            `json:"ParentSpanId"`
			SpanName           string            `json:"SpanName"`
			ServiceName        string            `json:"ServiceName"`
			SpanAttributes     map[string]string `json:"SpanAttributes"`
			ResourceAttributes map[string]string `json:"ResourceAttributes"`
		}
		if err := dec.Decode(&row); err != nil {
			return nil, fmt.Errorf("query spans by %s %s: decode row: %w", param, value, err)
		}
		spans = append(spans, Span{
			TraceID:            row.TraceID,
			SpanID:             row.SpanID,
			ParentSpanID:       row.ParentSpanID,
			Name:               row.SpanName,
			ServiceName:        row.ServiceName,
			Attributes:         row.SpanAttributes,
			ResourceAttributes: row.ResourceAttributes,
		})
	}
	return spans, nil
}

// The OTLP/JSON encoding of an export request, as much of it as SendSpan
// uses. IDs are hex and timestamps decimal strings, as OTLP/JSON requires.
type (
	otlpRequest struct {
		ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
	}
	otlpResourceSpans struct {
		Resource   otlpResource     `json:"resource"`
		ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
	}
	otlpResource struct {
		Attributes []otlpKeyValue `json:"attributes"`
	}
	otlpScopeSpans struct {
		Scope otlpScope  `json:"scope"`
		Spans []otlpSpan `json:"spans"`
	}
	otlpScope struct {
		Name string `json:"name"`
	}
	otlpSpan struct {
		TraceID           string         `json:"traceId"`
		SpanID            string         `json:"spanId"`
		Name              string         `json:"name"`
		Kind              int            `json:"kind"`
		StartTimeUnixNano string         `json:"startTimeUnixNano"`
		EndTimeUnixNano   string         `json:"endTimeUnixNano"`
		Attributes        []otlpKeyValue `json:"attributes"`
	}
	otlpKeyValue struct {
		Key   string    `json:"key"`
		Value otlpValue `json:"value"`
	}
	otlpValue struct {
		StringValue string `json:"stringValue"`
	}
)

// otlpSpanKindInternal is SPAN_KIND_INTERNAL.
const otlpSpanKindInternal = 1

func otlpString(key, value string) otlpKeyValue {
	return otlpKeyValue{Key: key, Value: otlpValue{StringValue: value}}
}
