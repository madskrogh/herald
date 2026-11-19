package endpointtest_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.uber.org/goleak"

	"github.com/madskrogh/herald/internal/endpointtest"
	"github.com/madskrogh/herald/internal/signing"
)

// TestMain checks that a closed Server leaves no goroutine behind.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestEndpointRecordsEachAttempt(t *testing.T) {
	t.Parallel()
	srv := startServer(t)
	ep := srv.NewEndpoint(endpointtest.Succeed())

	post(t, ep.URL, "msg_1", `{"invoice": 42}`)
	post(t, ep.URL, "msg_2", `{"invoice": 43}`)

	got, err := ep.WaitAttempts(t.Context(), 2)
	if err != nil {
		t.Fatalf("WaitAttempts: %v", err)
	}
	want := []endpointtest.Attempt{
		{EndpointURL: ep.URL, Number: 1, WebhookID: "msg_1", Body: []byte(`{"invoice": 42}`)},
		{EndpointURL: ep.URL, Number: 2, WebhookID: "msg_2", Body: []byte(`{"invoice": 43}`)},
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(endpointtest.Attempt{}, "Header", "ReceivedAt", "SignatureErr", "DeliveryNumber", "Answer")); diff != "" {
		t.Errorf("Attempts (-want +got):\n%s", diff)
	}
	for _, a := range got {
		if a.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Attempt %d: Content-Type = %q, want the request's", a.Number, a.Header.Get("Content-Type"))
		}
		if a.ReceivedAt.IsZero() {
			t.Errorf("Attempt %d: ReceivedAt is zero", a.Number)
		}
	}
}

func TestEndpointAnswersAsScripted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		respond endpointtest.Respond
		want    []int
	}{
		{"succeed", endpointtest.Succeed(), []int{200, 200, 200}},
		{"fail twice", endpointtest.FailTimes(2), []int{500, 500, 200}},
		{"fail never", endpointtest.FailTimes(0), []int{200, 200, 200}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := startServer(t)
			ep := srv.NewEndpoint(tt.respond)

			var got []int
			for range tt.want {
				got = append(got, post(t, ep.URL, "msg_1", `{}`))
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("status codes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestEndpointAnswersLikeARealOne checks the answers a real Endpoint gives
// besides a bare status code, as an HTTP client sees them.
func TestEndpointAnswersLikeARealOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		answer endpointtest.Answer
		// check checks what the client got.
		check func(t *testing.T, resp *http.Response, body []byte, err error)
	}{
		{
			name:   "redirect",
			answer: endpointtest.Answer{Status: http.StatusFound, Header: http.Header{"Location": {"http://example.com/elsewhere"}}},
			check: func(t *testing.T, resp *http.Response, _ []byte, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("POST: %v", err)
				}
				if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "http://example.com/elsewhere" {
					t.Errorf("response = %d, Location %q; want 302 to http://example.com/elsewhere", resp.StatusCode, resp.Header.Get("Location"))
				}
			},
		},
		{
			name:   "oversized body",
			answer: endpointtest.Answer{Status: http.StatusOK, Body: bytes.Repeat([]byte("x"), 1<<20)},
			check: func(t *testing.T, resp *http.Response, body []byte, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("POST: %v", err)
				}
				if resp.StatusCode != http.StatusOK || len(body) != 1<<20 {
					t.Errorf("response = %d with %d bytes, want 200 with 1 MiB", resp.StatusCode, len(body))
				}
			},
		},
		{
			name:   "reset",
			answer: endpointtest.Answer{Reset: true},
			check: func(t *testing.T, _ *http.Response, _ []byte, err error) {
				t.Helper()
				if !errors.Is(err, syscall.ECONNRESET) {
					t.Errorf("POST: %v, want the connection reset", err)
				}
			},
		},
		{
			name:   "malformed",
			answer: endpointtest.Answer{Malformed: true},
			check: func(t *testing.T, _ *http.Response, _ []byte, err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), "malformed HTTP") {
					t.Errorf("POST: %v, want a malformed response", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := startServer(t)
			ep := srv.NewEndpoint(func(context.Context, endpointtest.Attempt) endpointtest.Answer { return tt.answer })
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ep.URL, strings.NewReader(`{}`))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Do(req)
			var body []byte
			if err == nil {
				body, err = io.ReadAll(resp.Body)
				resp.Body.Close()
			}
			tt.check(t, resp, body, err)

			got, err := ep.WaitAttempts(t.Context(), 1)
			if err != nil {
				t.Fatalf("WaitAttempts: %v", err)
			}
			if diff := cmp.Diff(tt.answer, got[0].Answer); diff != "" {
				t.Errorf("recorded Answer (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTLSEndpointFailsTheHandshake checks that a TLS Endpoint's certificate
// fails verification, and that the Endpoint records each handshake.
func TestTLSEndpointFailsTheHandshake(t *testing.T) {
	t.Parallel()
	srv := startServer(t)
	ep, err := srv.NewTLSEndpoint(t.Context())
	if err != nil {
		t.Fatalf("NewTLSEndpoint: %v", err)
	}
	if !strings.HasPrefix(ep.URL, "https://") {
		t.Errorf("URL = %q, want https", ep.URL)
	}

	for range 2 {
		_, err := postStatus(t.Context(), ep.URL, "msg_1", `{}`)
		var unknown x509.UnknownAuthorityError
		if !errors.As(err, &unknown) {
			t.Errorf("POST: %v, want a certificate signed by an unknown authority", err)
		}
	}

	got, err := ep.WaitAttempts(t.Context(), 2)
	if err != nil {
		t.Fatalf("WaitAttempts: %v", err)
	}
	want := []endpointtest.Attempt{
		{EndpointURL: ep.URL, Number: 1, DeliveryNumber: 1, Handshake: true},
		{EndpointURL: ep.URL, Number: 2, DeliveryNumber: 2, Handshake: true},
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(endpointtest.Attempt{}, "ReceivedAt")); diff != "" {
		t.Errorf("Attempts (-want +got):\n%s", diff)
	}
}

// TestEndpointNumbersAttemptsPerDelivery checks that an Attempt's
// DeliveryNumber counts only the Attempts with its webhook-id: an Endpoint
// gets one Delivery per Message.
func TestEndpointNumbersAttemptsPerDelivery(t *testing.T) {
	t.Parallel()
	srv := startServer(t)
	ep := srv.NewEndpoint(endpointtest.Succeed())

	for _, id := range []string{"msg_1", "msg_2", "msg_1", "msg_1", "msg_2"} {
		post(t, ep.URL, id, `{}`)
	}

	attempts, err := ep.WaitAttempts(t.Context(), 5)
	if err != nil {
		t.Fatalf("WaitAttempts: %v", err)
	}
	var got []int
	for _, a := range attempts {
		got = append(got, a.DeliveryNumber)
	}
	if diff := cmp.Diff([]int{1, 1, 2, 3, 2}, got); diff != "" {
		t.Errorf("DeliveryNumbers (-want +got):\n%s", diff)
	}
}

// TestEndpointsAnswerOnTheirOwn checks that each Endpoint counts only its
// own Attempts.
func TestEndpointsAnswerOnTheirOwn(t *testing.T) {
	t.Parallel()
	srv := startServer(t)
	first := srv.NewEndpoint(endpointtest.FailTimes(1))
	second := srv.NewEndpoint(endpointtest.FailTimes(1))

	got := []int{
		post(t, first.URL, "msg_1", `{}`),
		post(t, first.URL, "msg_1", `{}`),
		post(t, second.URL, "msg_2", `{}`),
	}

	if diff := cmp.Diff([]int{500, 200, 500}, got); diff != "" {
		t.Errorf("status codes (-want +got):\n%s", diff)
	}
}

// TestEndpointTimesOutAttempts checks that the first Attempts get no answer
// until their client gives up, and the rest succeed.
func TestEndpointTimesOutAttempts(t *testing.T) {
	t.Parallel()
	srv := startServer(t)
	ep := srv.NewEndpoint(endpointtest.TimeOutTimes(1))

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if s, err := postStatus(ctx, ep.URL, "msg_1", `{}`); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("first Attempt: %d, %v; want no answer before the client gives up", s, err)
	}
	if s := post(t, ep.URL, "msg_1", `{}`); s != http.StatusOK {
		t.Errorf("second Attempt: %d, want 200", s)
	}
}

func TestEndpointHoldsAttemptsUntilReleased(t *testing.T) {
	t.Parallel()
	srv := startServer(t)
	release := make(chan struct{})
	ep := srv.NewEndpoint(endpointtest.Hold(release))
	// The goroutines report back rather than call t, which they may outlive
	// if the test fails: closing the Server then ends their requests.
	type result struct {
		status int
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			s, err := postStatus(t.Context(), ep.URL, "msg_1", `{}`)
			results <- result{s, err}
		}()
	}

	if _, err := ep.WaitAttempts(t.Context(), 2); err != nil {
		t.Fatalf("WaitAttempts: %v", err)
	}
	// Both Attempts have arrived, so a response would have been written by
	// now unless held. Waiting longer could only make the check pass by luck.
	select {
	case r := <-results:
		t.Fatalf("Attempt answered before release: %d, %v", r.status, r.err)
	default:
	}
	close(release)

	for range 2 {
		if r := <-results; r.err != nil || r.status != http.StatusOK {
			t.Errorf("after release: %d, %v; want 200", r.status, r.err)
		}
	}
}

func TestEndpointVerifiesSignatures(t *testing.T) {
	t.Parallel()
	secret := signing.NewSecret()
	tests := []struct {
		name string
		// secret is the Endpoint's signing secret, unset if empty.
		secret string
		// signWith are the secrets the Attempt is signed with; none leaves it
		// unsigned.
		signWith []string
		wantErr  bool
		// wantIs is the error SignatureErr must match, if any in particular.
		wantIs error
	}{
		{name: "signed with its secret", secret: secret, signWith: []string{secret}},
		{name: "signed with its secret among others", secret: secret, signWith: []string{signing.NewSecret(), secret}},
		{name: "signed with another secret", secret: secret, signWith: []string{signing.NewSecret()}, wantErr: true, wantIs: signing.ErrNoMatchingSignature},
		{name: "unsigned", secret: secret, wantErr: true},
		{name: "no secret set", signWith: []string{secret}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := startServer(t)
			ep := srv.NewEndpoint(endpointtest.Succeed())
			if tt.secret != "" {
				ep.SetSecret(tt.secret)
			}
			payload := []byte(`{"invoice": 42}`)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ep.URL, bytes.NewReader(payload))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			if len(tt.signWith) > 0 {
				h, err := signing.Sign("msg_1", time.Now(), payload, tt.signWith)
				if err != nil {
					t.Fatalf("Sign: %v", err)
				}
				req.Header = h
			}
			send(t, req)

			got, err := ep.WaitAttempts(t.Context(), 1)
			if err != nil {
				t.Fatalf("WaitAttempts: %v", err)
			}
			switch gotErr := got[0].SignatureErr; {
			case !tt.wantErr && gotErr != nil:
				t.Errorf("SignatureErr = %v, want nil", gotErr)
			case tt.wantErr && gotErr == nil:
				t.Error("SignatureErr = nil, want an error")
			case tt.wantIs != nil && !errors.Is(gotErr, tt.wantIs):
				t.Errorf("SignatureErr = %v, want %v", gotErr, tt.wantIs)
			}
		})
	}
}

// startServer starts a Server on a loopback port and closes it when the test
// ends.
func startServer(t *testing.T) *endpointtest.Server {
	t.Helper()
	srv, err := endpointtest.Start(t.Context(), "127.0.0.1:0", "127.0.0.1")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// post sends an unsigned Attempt to url and returns the status code of the
// response.
func post(t *testing.T, url, webhookID, body string) int {
	t.Helper()
	status, err := postStatus(t.Context(), url, webhookID, body)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

// postStatus sends an unsigned Attempt to url and returns the status code of
// the response.
func postStatus(ctx context.Context, url, webhookID, body string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		return 0, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", webhookID)
	return do(req)
}

// send sends req and returns the status code of the response.
func send(t *testing.T, req *http.Request) int {
	t.Helper()
	status, err := do(req)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

// do sends req and returns the status code of the response.
func do(req *http.Request) (int, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("POST %s: %w", req.URL, err)
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
