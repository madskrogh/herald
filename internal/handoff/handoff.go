// Package handoff names where the relay in herald-api hands Batches off to
// herald-dispatcher over NATS (ADR 0002). What a Batch and its reply hold is
// the protobuf package herald.handoff.v1 (internal/gen/herald/handoff/v1).
package handoff

const (
	// Subject is the NATS subject the relay sends Batches to, as requests.
	Subject = "herald.handoff.v1"
	// QueueGroup is the queue group every dispatcher subscribes in, so that
	// each Batch goes to one of them.
	QueueGroup = "herald-dispatcher"
)
