// Package telemetry is Herald's tracing: the names and attribute keys of the
// spans every service emits, which follow the span schema
// (spec/span-schema.md), the trace context stored with Messages and Deliveries, and the
// setup that exports spans to the Collector (spec/observability.md).
package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/codes"
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
	// Outcome is how the span's work ended. Outcomes lists the values each
	// span uses.
	Outcome attribute.Key = "herald.outcome"
	// StatusCode is an Attempt's response status, or 0 if there was no
	// response.
	StatusCode attribute.Key = "herald.status_code"
	// LatencyMs is how long an Attempt's request took, up to the response
	// headers or the error, in milliseconds.
	LatencyMs attribute.Key = "herald.latency_ms"
	// LatenessMs is how long after its Delivery fell due an Attempt
	// started, in milliseconds.
	LatenessMs attribute.Key = "herald.lateness_ms"
	// BatchSize is how many Messages the Batch holds.
	BatchSize attribute.Key = "herald.batch_size"
	// DeliveryCount is how many Deliveries a Message has.
	DeliveryCount attribute.Key = "herald.delivery_count"
	// Error says why the work failed, or is empty.
	Error attribute.Key = "herald.error"
)

// The values of Outcome. Outcomes says which each span uses.
const (
	// OutcomeHandedOff: the Message was handed off. For a Batch, its round
	// committed: each of its Messages was handed off or parked, and their
	// own spans say which.
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
// carries exactly these. A test checks it against the span schema in the
// spec, which is what the code must follow; nothing else reads it.
var Schema = map[string][]attribute.Key{
	SpanTenantAPICall:   {RPCMethod, TenantID, Outcome, Error},
	SpanHandoffBatch:    {BatchSize, Outcome, Error},
	SpanHandoff:         {TenantID, ApplicationID, MessageID, BatchSize, DeliveryCount, Outcome, Error},
	SpanStoreDeliveries: {TenantID, ApplicationID, MessageID, BatchSize, DeliveryCount, Outcome, Error},
	SpanAttempt:         {TenantID, ApplicationID, EndpointID, MessageID, DeliveryID, AttemptNumber, Outcome, StatusCode, LatencyMs, LatenessMs, Error},
}

// Outcomes lists the values of Outcome each span Herald emits takes. A test
// checks it against the span schema in the spec; nothing else reads it.
var Outcomes = map[string][]string{
	SpanTenantAPICall:   grpcCodes(),
	SpanHandoffBatch:    {OutcomeHandedOff, OutcomeFailed},
	SpanHandoff:         {OutcomeHandedOff, OutcomeParked, OutcomeFailed},
	SpanStoreDeliveries: {OutcomeStored, OutcomeFailed},
	SpanAttempt:         {OutcomeSucceeded, OutcomePending, OutcomeFailed, OutcomeCutOff, OutcomeNotRecorded},
}

// grpcCodes returns the name of each gRPC status code, the outcome of a
// Tenant API call.
func grpcCodes() []string {
	var names []string
	for c := codes.OK; c <= codes.Unauthenticated; c++ {
		names = append(names, c.String())
	}
	return names
}
