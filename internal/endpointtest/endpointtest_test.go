package endpointtest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
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
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(endpointtest.Attempt{}, "Header", "ReceivedAt", "SignatureErr")); diff != "" {
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
