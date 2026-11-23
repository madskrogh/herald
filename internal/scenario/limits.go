package scenario

import "time"

// The limits the scenario invariants hold Herald to, each named after its
// row in spec/limits.md, where its reason is. TestLimitsFollowTheSpec
// compares them with the spec.
const (
	// attemptTimeout is attempt-timeout (R-7).
	attemptTimeout = 15 * time.Second
	// maxAttempts is max-attempts (R-12).
	maxAttempts = 8
	// maxInFlight is max-in-flight-per-endpoint (R-19).
	maxInFlight = 4
	// maxDelay is max-delay: handoff lag and Attempt lateness with no
	// fault (R-71, R-72).
	maxDelay = 2 * time.Second
	// maxDelayInFault is max-delay-in-fault: the same under a fault (R-73,
	// R-74).
	maxDelayInFault = 15 * time.Second
	// eventTypeBytes is event-type-bytes (R-35).
	eventTypeBytes = 128
	// filterPatternBytes is filter-pattern-bytes (R-31).
	filterPatternBytes = 128
	// maxFilterPatterns is max-filter-patterns (R-31).
	maxFilterPatterns = 32
	// retryJitter is retry-jitter, as a fraction of each delay (R-11).
	retryJitter = 0.1
)

// retryDelays is retry-delays: the delay before each retry, after the 1st
// to the 7th Attempt fails (R-11).
var retryDelays = []time.Duration{
	5 * time.Second, time.Minute, 10 * time.Minute, time.Hour,
	3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
}
