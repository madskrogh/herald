// Package telemetry is Herald's tracing: the span schema every service's spans
// follow, the trace context stored with Messages and Deliveries, and the
// setup that exports spans to the Collector (docs/design.md, Observability).
package telemetry

import (
	"fmt"
	"slices"

	"go.opentelemetry.io/otel/attribute"
)

// The names of the spans Herald emits. Schema lists each one's attributes.
const (
	// SpanTenantAPICall is one call to the Tenant API, over gRPC or REST. It
	// is the root of a Message's trace.
	SpanTenantAPICall = "tenant_api.call"
	// SpanHandoffBatch is one Batch the relay sends: a trace of its own,
	// with a link to the handoff span of each Message in it.
	SpanHandoffBatch = "handoff.batch"
	// SpanHandoff is one Message in a Batch, in the Message's trace.
	SpanHandoff = "handoff"
	// SpanStoreDeliveries is a dispatcher storing one Message's Deliveries
	// from a Batch. Each Delivery keeps its context, so that every Attempt
	// for it is its child.
	SpanStoreDeliveries = "store_deliveries"
	// SpanAttempt is one Attempt.
	SpanAttempt = "attempt"
)

// The attribute keys of Herald's spans. IDs are UUIDs in canonical text form.
// An attribute that doesn't apply to a span is set to its zero value, never
// left out, so a check can tell a missing attribute from an empty one.
const (
	// RPCMethod is the Tenant API method called, as a full gRPC method name.
	RPCMethod attribute.Key = "rpc.method"
	TenantID  attribute.Key = "herald.tenant_id"
	// ApplicationID is empty for a Message stored before Messages carried
	// their Application across the handoff.
	ApplicationID attribute.Key = "herald.application_id"
	EndpointID    attribute.Key = "herald.endpoint_id"
	MessageID     attribute.Key = "herald.message_id"
	DeliveryID    attribute.Key = "herald.delivery_id"
	// AttemptNumber counts a Delivery's Attempts from 1.
	AttemptNumber attribute.Key = "herald.attempt"
	// Outcome is how the span's work ended. The values each span uses are
	// listed with it in Schema.
	Outcome attribute.Key = "herald.outcome"
	// StatusCode is an Attempt's response status, or 0 if there was no
	// response.
	StatusCode attribute.Key = "herald.status_code"
	// LatencyMs is how long an Attempt's request took, up to the response
	// headers or the error, in milliseconds.
	LatencyMs attribute.Key = "herald.latency_ms"
	// BatchSize is how many Messages the Batch holds.
	BatchSize attribute.Key = "herald.batch_size"
	// DeliveryCount is how many Deliveries a Message has.
	DeliveryCount attribute.Key = "herald.delivery_count"
	// Error says why the work failed, or is empty.
	Error attribute.Key = "herald.error"
)

// The values of Outcome. Schema says which each span uses.
const (
	// OutcomeHandedOff: the Message, or every Message of the Batch, was
	// handed off.
	OutcomeHandedOff = "handed_off"
	// OutcomeParked: the Message was parked.
	OutcomeParked = "parked"
	// OutcomeStored: the Message's Deliveries were stored.
	OutcomeStored = "stored"
	// OutcomeFailed: the work failed, and the error says why. For an
	// Attempt, the Delivery's retry schedule ran out.
	OutcomeFailed = "failed"
	// OutcomeSucceeded and OutcomePending: the Delivery's state after an
	// Attempt.
	OutcomeSucceeded = "succeeded"
	OutcomePending   = "pending"
	// OutcomeCutOff: shutdown cut the Attempt off, and it isn't counted.
	OutcomeCutOff = "cut_off"
	// OutcomeNotRecorded: the Attempt couldn't be recorded, and the error
	// says why.
	OutcomeNotRecorded = "not_recorded"
)

// Schema lists the attribute keys of each span Herald emits. Every span
// carries exactly these, so renaming or dropping one is a visible change
// that fails the tests, not a query that silently reads nothing.
var Schema = map[string][]attribute.Key{
	// Outcome: the call's gRPC status code, such as OK or NotFound. The
	// Tenant is empty for a call that isn't authenticated.
	SpanTenantAPICall: {RPCMethod, TenantID, Outcome, Error},
	// Outcome: handed_off, or failed when the Batch's round failed.
	SpanHandoffBatch: {BatchSize, Outcome, Error},
	// Outcome: handed_off, parked, or failed when the round failed and the
	// Message stays in the Outbox.
	SpanHandoff: {TenantID, ApplicationID, MessageID, BatchSize, DeliveryCount, Outcome, Error},
	// Outcome: stored, or failed when the Batch was not stored.
	SpanStoreDeliveries: {TenantID, ApplicationID, MessageID, BatchSize, DeliveryCount, Outcome, Error},
	// Outcome: the Delivery's state after the Attempt (succeeded, pending or
	// failed); cut_off when shutdown cut the Attempt off and it isn't
	// counted; or not_recorded when the Attempt couldn't be recorded, and
	// the error says why.
	SpanAttempt: {TenantID, ApplicationID, EndpointID, MessageID, DeliveryID, AttemptNumber, Outcome, StatusCode, LatencyMs, Error},
}

// CheckSpan reports whether a span named name with attributes keys matches
// the schema: a span Schema lists, with each of its keys once and no other.
func CheckSpan(name string, keys []string) error {
	want, ok := Schema[name]
	if !ok {
		return fmt.Errorf("span %q is not in the schema", name)
	}
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			return fmt.Errorf("span %q: attribute %q twice", name, k)
		}
		seen[k] = true
		if !slices.Contains(want, attribute.Key(k)) {
			return fmt.Errorf("span %q: attribute %q is not in the schema", name, k)
		}
	}
	for _, k := range want {
		if !seen[string(k)] {
			return fmt.Errorf("span %q: attribute %q is missing", name, k)
		}
	}
	return nil
}
