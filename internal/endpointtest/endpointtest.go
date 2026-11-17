// Package endpointtest serves fake Endpoints for end-to-end tests: each
// answers Attempts as a script says, verifies their signatures and records
// them.
package endpointtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/madskrogh/herald/internal/signing"
)

// Server serves fake Endpoints over HTTP.
type Server struct {
	srv     *http.Server
	baseURL string

	mu        sync.Mutex
	endpoints []*Endpoint
}

// Start starts a Server listening on addr. Endpoint URLs name host, which is
// how Herald reaches the Server: from a container, that is not the address it
// listens on.
func Start(ctx context.Context, addr, host string) (*Server, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	s := &Server{baseURL: "http://" + net.JoinHostPort(host, strconv.Itoa(port))}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /endpoints/{n}", s.serveAttempt)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go s.srv.Serve(lis) //nolint:errcheck // Returns ErrServerClosed once Close is called.
	return s, nil
}

// Close stops the Server, cancelling any Attempt it is still answering.
func (s *Server) Close() {
	s.srv.Close()
}

// NewEndpoint adds an Endpoint that answers its Attempts with respond.
func (s *Server) NewEndpoint(respond Respond) *Endpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := &Endpoint{
		URL:     fmt.Sprintf("%s/endpoints/%d", s.baseURL, len(s.endpoints)),
		respond: respond,
		changed: make(chan struct{}),
	}
	s.endpoints = append(s.endpoints, e)
	return e
}

func (s *Server) serveAttempt(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	s.mu.Lock()
	if err != nil || n < 0 || n >= len(s.endpoints) {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	e := s.endpoints[n]
	s.mu.Unlock()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	number := e.record(Attempt{
		EndpointURL: e.URL,
		ReceivedAt:  time.Now(),
		WebhookID:   r.Header.Get("webhook-id"),
		Header:      r.Header.Clone(),
		Body:        body,
	})
	w.WriteHeader(e.respond(r.Context(), number))
}

// Endpoint is a fake Endpoint.
type Endpoint struct {
	// URL is the URL to register for the Endpoint.
	URL string

	respond Respond

	mu       sync.Mutex
	secret   string
	attempts []Attempt
	// changed is closed, and replaced, whenever an Attempt is recorded.
	changed chan struct{}
}

// record verifies a's signature, numbers a and adds it to the Endpoint's
// Attempts, and returns its number.
func (e *Endpoint) record(a Attempt) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	a.SignatureErr = errNoSecret
	if e.secret != "" {
		a.SignatureErr = signing.Verify(e.secret, a.Header, a.Body, a.ReceivedAt)
	}
	a.Number = len(e.attempts) + 1
	e.attempts = append(e.attempts, a)
	close(e.changed)
	e.changed = make(chan struct{})
	return a.Number
}

// SetSecret sets the signing secret the Endpoint verifies its Attempts with:
// the one the Tenant API returned when the Endpoint was created.
func (e *Endpoint) SetSecret(secret string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.secret = secret
}

// WaitAttempts waits until the Endpoint has received at least n Attempts and
// returns all of them, in the order received.
func (e *Endpoint) WaitAttempts(ctx context.Context, n int) ([]Attempt, error) {
	for {
		e.mu.Lock()
		attempts, changed := append([]Attempt(nil), e.attempts...), e.changed
		e.mu.Unlock()
		if len(attempts) >= n {
			return attempts, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return attempts, fmt.Errorf("%s received %d Attempts, waiting for %d: %w", e.URL, len(attempts), n, context.Cause(ctx))
		}
	}
}

// Attempt is an Attempt the Endpoint received.
type Attempt struct {
	// EndpointURL is the URL of the Endpoint that received the Attempt.
	EndpointURL string
	// Number counts the Endpoint's Attempts from 1, in the order received.
	Number     int
	ReceivedAt time.Time
	WebhookID  string
	Header     http.Header
	Body       []byte
	// SignatureErr is why the signature didn't verify, or nil if it did.
	SignatureErr error
}

// Respond returns the status code for an Endpoint's nth Attempt, counting
// from 1. ctx is the request's, cancelled if the client gives up.
type Respond func(ctx context.Context, n int) int

// Succeed answers every Attempt with 200.
func Succeed() Respond {
	return func(context.Context, int) int { return http.StatusOK }
}

// Hold answers no Attempt until release is closed, then answers each with
// 200. An Attempt whose client gives up first gets no answer.
func Hold(release <-chan struct{}) Respond {
	return func(ctx context.Context, _ int) int {
		select {
		case <-release:
			return http.StatusOK
		case <-ctx.Done():
			// The client has gone, so the status is never sent.
			return http.StatusServiceUnavailable
		}
	}
}

// FailTimes answers the first n Attempts with 500 and the rest with 200.
func FailTimes(n int) Respond {
	return func(_ context.Context, number int) int {
		if number <= n {
			return http.StatusInternalServerError
		}
		return http.StatusOK
	}
}

// errNoSecret is the SignatureErr of an Attempt to an Endpoint whose signing
// secret hasn't been set.
var errNoSecret = errors.New("endpoint has no signing secret to verify with")
