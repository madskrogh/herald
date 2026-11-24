package scenario

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/madskrogh/herald/internal/endpointtest"
)

const (
	// failures is how many of each Delivery's first Attempts a Fail
	// Endpoint answers with 500. TimeOut, Reset and Malformed get only the
	// first one wrong.
	failures = 2
	// slowAnswer is how long a Slow Endpoint takes to answer: well within
	// the 15 s timeout.
	slowAnswer = 2 * time.Second
	// redirectTo is where a Redirect Endpoint points. Herald doesn't follow
	// it, so it needn't exist.
	redirectTo = "/moved"
)

// oversizedBody is what an Oversized Endpoint answers with: far more than
// the start of the response body that Herald keeps.
var oversizedBody = bytes.Repeat([]byte("x"), 1<<20)

// respond returns how an Endpoint with behaviour b answers each Attempt.
func respond(b Behaviour) endpointtest.Respond {
	ok := endpointtest.Status(http.StatusOK)
	return func(ctx context.Context, a endpointtest.Attempt) endpointtest.Answer {
		first := a.DeliveryNumber == 1
		switch b {
		case Fail:
			if a.DeliveryNumber <= failures {
				return endpointtest.Status(http.StatusInternalServerError)
			}
		case Slow:
			select {
			case <-time.After(slowAnswer):
			case <-ctx.Done():
				return endpointtest.Unanswered(ctx)
			}
		case TimeOut:
			if first {
				return endpointtest.Unanswered(ctx)
			}
		case Reset:
			if first {
				return endpointtest.Answer{Kind: endpointtest.AnswerReset}
			}
		case Malformed:
			if first {
				return endpointtest.Answer{Kind: endpointtest.AnswerMalformed}
			}
		case Oversized:
			return endpointtest.Answer{Status: http.StatusOK, Body: oversizedBody}
		case Redirect:
			return endpointtest.Answer{Status: http.StatusFound, Header: http.Header{"Location": {redirectTo}}}
		case Flap:
			// Turns with each Attempt, starting from the Message's place in
			// the scenario, so neighbouring Messages flap out of step.
			if (messageNumber(a.Body)+a.DeliveryNumber)%2 == 1 {
				return endpointtest.Status(http.StatusServiceUnavailable)
			}
		}
		return ok
	}
}

// messageNumber returns the number in the name of the Message whose payload
// is body, such as 17 for m017, or 0 if body names none.
func messageNumber(body []byte) int {
	n, err := strconv.Atoi(strings.TrimPrefix(payloadMessage(string(body)), "m"))
	if err != nil {
		return 0
	}
	return n
}
