// Package scenario runs generated traffic against a Herald stack and records
// what its Endpoints receive, so that a later build can be compared with a
// committed baseline. The scenario stands in for production traffic, and the
// recording is taken outside Herald, at the Endpoints, so a change to
// Herald's instrumentation can't change it (docs/testing.md).
package scenario

// Scenario is the traffic of one run, generated from its Seed.
type Scenario struct {
	Seed         uint64
	Applications []Application
	// Phases run one after another. Each finishes, with every Delivery of
	// its Messages succeeded or failed, before the next starts.
	Phases []Phase
}

// Application is an Application the scenario creates.
type Application struct {
	Name      string
	Endpoints []Endpoint
}

// Endpoint is an Endpoint the scenario registers, and how it answers.
type Endpoint struct {
	Name      string
	Filter    []string
	Behaviour Behaviour
}

// Behaviour is how an Endpoint answers its Attempts, like a real Endpoint
// does.
type Behaviour string

// The Endpoint behaviours. Those that recover misbehave on each Delivery's
// first Attempts and then succeed.
const (
	// Succeed answers 200.
	Succeed Behaviour = "succeed"
	// Fail answers 500 and then recovers.
	Fail Behaviour = "fail"
	// Slow answers 200 after a few seconds, well within the timeout.
	Slow Behaviour = "slow"
	// TimeOut leaves the first Attempt unanswered and then recovers.
	TimeOut Behaviour = "time out"
	// Reset resets the connection and then recovers.
	Reset Behaviour = "reset"
	// Malformed answers with something that isn't HTTP and then recovers.
	Malformed Behaviour = "malformed"
	// Oversized answers 200 with a body far larger than Herald keeps.
	Oversized Behaviour = "oversized"
	// Redirect always answers 302.
	Redirect Behaviour = "redirect"
	// TLSError serves a certificate no one trusts.
	TLSError Behaviour = "tls error"
	// Flap succeeds and fails by turns, from one Delivery to the next.
	Flap Behaviour = "flap"
)

// Fault is something that goes wrong in the stack during a phase.
type Fault string

// The faults. A phase without one has the empty Fault.
const (
	// DispatcherRestarted restarts herald-dispatcher while an Attempt is in
	// flight, which is cut off.
	DispatcherRestarted Fault = "dispatcher restarted"
	// NATSPaused pauses NATS while the phase's Messages are sent, so they
	// wait in the Outbox.
	NATSPaused Fault = "nats paused"
	// PostgresSlowed starves Postgres of CPU while the phase runs.
	PostgresSlowed Fault = "postgres slowed"
)

// Phase is a part of the scenario: Messages sent while a Fault, if any, is
// in effect.
type Phase struct {
	Fault    Fault
	Messages []Message
	// HeldEndpoint, for DispatcherRestarted, names the TimeOut Endpoint
	// whose unanswered Attempt is in flight when the dispatcher restarts.
	HeldEndpoint string
}

// Message is a Message the scenario sends.
type Message struct {
	Name           string
	Application    string
	EventType      string
	Payload        string
	IdempotencyKey string
}
