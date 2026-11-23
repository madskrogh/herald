// Package endpointtest serves fake Endpoints for end-to-end tests: each
// answers Attempts as a script says, verifies their signatures and records
// them.
package endpointtest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"math/big"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madskrogh/herald/internal/signing"
)

// Server serves fake Endpoints over HTTP.
type Server struct {
	srv     *http.Server
	baseURL string
	// ip is the address the Server listens on, and host the name Endpoint
	// URLs give it.
	ip, host string

	mu        sync.Mutex
	endpoints []*Endpoint
	// tlsServers serve the TLS Endpoints, one each.
	tlsServers []*http.Server
}

// Start starts a Server listening on addr. Endpoint URLs name host, which is
// how Herald reaches the Server: from a container, that is not the address it
// listens on.
func Start(ctx context.Context, addr, host string) (*Server, error) {
	ip, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	lis, baseURL, err := listen(ctx, addr, "http", host)
	if err != nil {
		return nil, err
	}
	s := &Server{baseURL: baseURL, ip: ip, host: host}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /endpoints/{n}", s.serveAttempt)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go s.srv.Serve(lis) //nolint:errcheck // Returns ErrServerClosed once Close is called.
	return s, nil
}

// Close stops the Server, cancelling any Attempt it is still answering.
func (s *Server) Close() {
	s.srv.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, srv := range s.tlsServers {
		srv.Close()
	}
}

// NewTLSEndpoint adds an Endpoint served over TLS with a certificate no one
// trusts, as an Endpoint with a broken certificate is. No Attempt gets past
// the TLS handshake, so the Endpoint records each handshake as an Attempt
// with no header or body. A handshake carries no webhook-id, so it can't
// tell Deliveries apart: give the Endpoint one Delivery.
func (s *Server) NewTLSEndpoint(ctx context.Context) (*Endpoint, error) {
	cert, err := selfSigned(s.host)
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	lis, baseURL, err := listen(ctx, net.JoinHostPort(s.ip, "0"), "https", s.host)
	if err != nil {
		return nil, err
	}
	e := &Endpoint{
		URL:     baseURL + "/",
		respond: Succeed(),
		changed: make(chan struct{}),
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			now := time.Now()
			e.record(Attempt{EndpointURL: e.URL, ReceivedAt: now, AnsweredAt: now, Handshake: true})
			return nil, nil
		},
	}
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		ReadHeaderTimeout: 10 * time.Second,
		// Every handshake fails, and each failure would be logged.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tlsServers = append(s.tlsServers, srv)
	go srv.Serve(tls.NewListener(lis, cfg)) //nolint:errcheck // Returns ErrServerClosed once Close is called.
	return e, nil
}

// listen listens on addr, and returns the listener and the URL that reaches
// it through host, with the given scheme and no path.
func listen(ctx context.Context, addr, scheme, host string) (net.Listener, string, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("listen on %s: %w", addr, err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	return lis, scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// selfSigned returns a certificate for host that signs itself.
func selfSigned(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.DNSNames, tmpl.IPAddresses = nil, []net.IP{ip}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
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
	a := e.record(Attempt{
		EndpointURL: e.URL,
		ReceivedAt:  time.Now(),
		WebhookID:   r.Header.Get("webhook-id"),
		Header:      r.Header.Clone(),
		Body:        body,
	})
	answer := e.respond(r.Context(), a)
	e.answered(a.Number, answer)
	switch answer.Kind {
	case AnswerNone:
		// The client has gone, so there is no one to answer.
	case AnswerReset:
		hijack(w, func(conn net.Conn) {
			// A zero linger makes Close send RST rather than FIN.
			if tcp, ok := conn.(*net.TCPConn); ok {
				tcp.SetLinger(0) //nolint:errcheck // Close still ends the connection.
			}
		})
	case AnswerMalformed:
		hijack(w, func(conn net.Conn) {
			conn.Write([]byte("this is not an HTTP response\r\n\r\n")) //nolint:errcheck // The client may have gone.
		})
	default:
		for name, values := range answer.Header {
			w.Header()[name] = values
		}
		w.WriteHeader(answer.Status)
		w.Write(answer.Body) //nolint:errcheck // The client may have gone.
	}
}

// hijack takes over the connection w answers on, passes it to f and closes
// it.
func hijack(w http.ResponseWriter, f func(net.Conn)) {
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		// Every connection the Server serves can be hijacked, so this is a
		// bug in the Server, not in what it serves.
		panic(fmt.Sprintf("hijack connection: %v", err))
	}
	f(conn)
	conn.Close()
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
// Attempts, and returns it as recorded.
func (e *Endpoint) record(a Attempt) Attempt {
	e.mu.Lock()
	defer e.mu.Unlock()
	a.SignatureErr = errNoSecret
	switch {
	case a.Handshake:
		// Nothing was signed.
		a.SignatureErr = nil
	case e.secret != "":
		a.SignatureErr = signing.Verify(e.secret, a.Header, a.Body, a.ReceivedAt)
	}
	a.Number = len(e.attempts) + 1
	a.DeliveryNumber = 1
	for _, prev := range e.attempts {
		if prev.WebhookID == a.WebhookID {
			a.DeliveryNumber++
		}
	}
	e.attempts = append(e.attempts, a)
	close(e.changed)
	e.changed = make(chan struct{})
	return a
}

// answered records the Answer to the Endpoint's Attempt with the given
// number.
func (e *Endpoint) answered(number int, answer Answer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts[number-1].Answer = answer
	e.attempts[number-1].AnsweredAt = time.Now()
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
	Number int
	// DeliveryNumber counts the Delivery's Attempts from 1: those the
	// Endpoint received with the same webhook-id. An Endpoint gets one
	// Delivery per Message.
	DeliveryNumber int
	ReceivedAt     time.Time
	WebhookID      string
	Header         http.Header
	Body           []byte
	// SignatureErr is why the signature didn't verify, or nil if it did or
	// the Attempt is a Handshake.
	SignatureErr error
	// Handshake says the Attempt got no further than the TLS handshake, so
	// it has no header or body.
	Handshake bool
	// Answer is how the Endpoint answered, once it has.
	Answer Answer
	// AnsweredAt is when the Endpoint answered, or found the client gone,
	// just before it wrote the answer: the end of the Attempt as the
	// Endpoint sees it. A Handshake is answered when it is received.
	AnsweredAt time.Time
}

// Respond returns the Answer to an Attempt. ctx is the request's, cancelled
// if the client gives up.
type Respond func(ctx context.Context, a Attempt) Answer

// Answer is how an Endpoint answers an Attempt. Status, Header and Body are
// the response's, when Kind is AnswerResponse.
type Answer struct {
	Kind   AnswerKind
	Status int
	Header http.Header
	Body   []byte
}

// AnswerKind is whether an Endpoint answers an Attempt with an HTTP
// response, or how it fails to.
type AnswerKind int

const (
	// AnswerResponse answers with an HTTP response.
	AnswerResponse AnswerKind = iota
	// AnswerNone says the client gave up before the Endpoint answered.
	AnswerNone
	// AnswerReset resets the connection instead of answering.
	AnswerReset
	// AnswerMalformed answers with bytes that aren't an HTTP response.
	AnswerMalformed
)

// String describes the Answer in words: "unanswered", "reset", "malformed",
// or the response's status code, headers and body size, such as
// "302, Location: /moved".
func (a Answer) String() string {
	switch a.Kind {
	case AnswerNone:
		return "unanswered"
	case AnswerReset:
		return "reset"
	case AnswerMalformed:
		return "malformed"
	}
	parts := []string{strconv.Itoa(a.Status)}
	for _, name := range slices.Sorted(maps.Keys(a.Header)) {
		parts = append(parts, name+": "+strings.Join(a.Header[name], ", "))
	}
	if len(a.Body) > 0 {
		parts = append(parts, fmt.Sprintf("%d-byte body", len(a.Body)))
	}
	return strings.Join(parts, ", ")
}

// Status answers with an empty response with the given status code.
func Status(code int) Answer {
	return Answer{Status: code}
}

// Unanswered waits until the client gives up on an Attempt, and returns the
// Answer that says so.
func Unanswered(ctx context.Context) Answer {
	<-ctx.Done()
	return Answer{Kind: AnswerNone}
}

// Succeed answers every Attempt with 200.
func Succeed() Respond {
	return func(context.Context, Attempt) Answer { return Status(http.StatusOK) }
}

// Hold answers no Attempt until release is closed, then answers each with
// 200. An Attempt whose client gives up first gets no answer.
func Hold(release <-chan struct{}) Respond {
	return func(ctx context.Context, _ Attempt) Answer {
		select {
		case <-release:
			return Status(http.StatusOK)
		case <-ctx.Done():
			return Unanswered(ctx)
		}
	}
}

// TimeOutTimes leaves the Endpoint's first n Attempts unanswered until their
// client gives up, and answers the rest with 200.
func TimeOutTimes(n int) Respond {
	return func(ctx context.Context, a Attempt) Answer {
		if a.Number <= n {
			return Unanswered(ctx)
		}
		return Status(http.StatusOK)
	}
}

// FailTimes answers the Endpoint's first n Attempts with 500 and the rest
// with 200.
func FailTimes(n int) Respond {
	return func(_ context.Context, a Attempt) Answer {
		if a.Number <= n {
			return Status(http.StatusInternalServerError)
		}
		return Status(http.StatusOK)
	}
}

// errNoSecret is the SignatureErr of an Attempt to an Endpoint whose signing
// secret hasn't been set.
var errNoSecret = errors.New("endpoint has no signing secret to verify with")
