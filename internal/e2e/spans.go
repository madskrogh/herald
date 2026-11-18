package e2e

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Span is one span, as sent to the Collector and as read back from
// ClickHouse.
type Span struct {
	// TraceID and SpanID are in hex, as OTLP/JSON and ClickHouse write them.
	TraceID     string
	SpanID      string
	Name        string
	ServiceName string
	Attributes  map[string]string
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
	_, err = s.post(ctx, s.otlpURL+"/v1/traces", "application/json", body)
	if err != nil {
		return fmt.Errorf("send span: %w", err)
	}
	return nil
}

// spansQuery selects one trace's spans. It takes the query parameter
// trace_id.
//
//go:embed spans.sql
var spansQuery string

// WaitSpans waits until ClickHouse holds spans of the trace with the given
// ID, and returns them, ordered by span ID. The Collector batches spans
// before it exports them, so they arrive a moment after they are sent.
func (s *Stack) WaitSpans(ctx context.Context, traceID string) ([]Span, error) {
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		spans, err := s.traceSpans(ctx, traceID)
		if err != nil {
			return nil, err
		}
		if len(spans) > 0 {
			return spans, nil
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("trace %s: waiting for its spans: %w", traceID, context.Cause(ctx))
		}
	}
}

// traceSpans reads the spans of one trace from ClickHouse.
func (s *Stack) traceSpans(ctx context.Context, traceID string) ([]Span, error) {
	u := s.clickHouseURL + "/?" + url.Values{"param_trace_id": {traceID}}.Encode()
	data, err := s.post(ctx, u, "text/plain", []byte(spansQuery))
	if err != nil {
		return nil, fmt.Errorf("query trace %s: %w", traceID, err)
	}
	var spans []Span
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var row struct {
			TraceID        string            `json:"TraceId"`
			SpanID         string            `json:"SpanId"`
			SpanName       string            `json:"SpanName"`
			ServiceName    string            `json:"ServiceName"`
			SpanAttributes map[string]string `json:"SpanAttributes"`
		}
		if err := dec.Decode(&row); err != nil {
			return nil, fmt.Errorf("query trace %s: decode row: %w", traceID, err)
		}
		spans = append(spans, Span{
			TraceID:     row.TraceID,
			SpanID:      row.SpanID,
			Name:        row.SpanName,
			ServiceName: row.ServiceName,
			Attributes:  row.SpanAttributes,
		})
	}
	return spans, nil
}

// post POSTs body to url and returns the response body, or an error for any
// status but 200 OK.
func (s *Stack) post(ctx context.Context, url, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", res.Status, data)
	}
	return data, nil
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
