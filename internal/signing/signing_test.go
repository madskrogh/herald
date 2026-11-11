package signing_test

import (
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/signing"
)

// The test vector published with the Standard Webhooks reference libraries.
const (
	vectorSecret    = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	vectorWebhookID = "msg_p5jXN8AQM9LWM0D4loKWxJek"
	vectorPayload   = `{"test": 2432232314}`
	vectorSignature = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
)

var vectorTimestamp = time.Unix(1614265330, 0)

// headers returns the three Standard Webhooks headers with the given values.
func headers(webhookID, timestamp, signature string) http.Header {
	return http.Header{
		"Webhook-Id":        {webhookID},
		"Webhook-Timestamp": {timestamp},
		"Webhook-Signature": {signature},
	}
}

func TestNewSecret(t *testing.T) {
	t.Parallel()

	first, second := signing.NewSecret(), signing.NewSecret()

	encoded, ok := strings.CutPrefix(first, "whsec_")
	if !ok {
		t.Fatalf("secret %q lacks the whsec_ prefix", first)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("secret %q is not base64 after the prefix: %v", first, err)
	}
	if len(key) != 24 {
		t.Errorf("secret %q decodes to %d bytes, want 24", first, len(key))
	}
	if first == second {
		t.Errorf("two calls returned the same secret %q", first)
	}
}

func TestSignMatchesPublishedVector(t *testing.T) {
	t.Parallel()

	got, err := signing.Sign(vectorWebhookID, vectorTimestamp, []byte(vectorPayload), []string{vectorSecret})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	want := headers(vectorWebhookID, "1614265330", vectorSignature)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Sign headers mismatch (-want +got):\n%s", diff)
	}
}

func TestSignWithSeveralSecrets(t *testing.T) {
	t.Parallel()

	// secondSecret is "herald-second-secret-24b"; its signature was computed
	// independently with openssl dgst -sha256 -hmac.
	const (
		secondSecret    = "whsec_aGVyYWxkLXNlY29uZC1zZWNyZXQtMjRi"
		secondSignature = "v1,2fISVdnj3oFp5U2Y2IUeN0LkXSK8ZSoBPKSRJVwMsGM="
	)
	secrets := []string{vectorSecret, secondSecret}

	h, err := signing.Sign(vectorWebhookID, vectorTimestamp, []byte(vectorPayload), secrets)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	want := vectorSignature + " " + secondSignature
	if got := h.Get("webhook-signature"); got != want {
		t.Errorf("webhook-signature = %q, want %q", got, want)
	}
	for _, secret := range secrets {
		if err := signing.Verify(secret, h, []byte(vectorPayload), vectorTimestamp); err != nil {
			t.Errorf("Verify with %s: %v", secret, err)
		}
	}
}

func TestSignRejectsBadSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		secrets []string
	}{
		{name: "no secrets", secrets: nil},
		{name: "missing prefix", secrets: []string{"MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"}},
		{name: "not base64", secrets: []string{"whsec_not base64!"}},
		{name: "empty key", secrets: []string{"whsec_"}},
		{name: "one bad among good", secrets: []string{vectorSecret, "whsec_"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := signing.Sign(vectorWebhookID, vectorTimestamp, []byte(vectorPayload), tt.secrets)
			if err == nil {
				t.Errorf("Sign = %v, want an error", h)
			}
		})
	}
}

func TestVerifyAcceptsPublishedVector(t *testing.T) {
	t.Parallel()

	h := headers(vectorWebhookID, "1614265330", vectorSignature)
	if err := signing.Verify(vectorSecret, h, []byte(vectorPayload), vectorTimestamp); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestVerifyRejectsMissingWebhookID(t *testing.T) {
	t.Parallel()

	h := headers(vectorWebhookID, "1614265330", vectorSignature)
	h.Del("webhook-id")
	if err := signing.Verify(vectorSecret, h, []byte(vectorPayload), vectorTimestamp); err == nil {
		t.Error("Verify = nil, want an error")
	}
}

func TestVerifyTimestamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		timestamp string
		now       time.Time
		wantErr   error
	}{
		{
			name:      "five minutes old",
			timestamp: "1614265330",
			now:       time.Unix(1614265630, 0),
			wantErr:   nil,
		},
		{
			name:      "five minutes ahead",
			timestamp: "1614265330",
			now:       time.Unix(1614265030, 0),
			wantErr:   nil,
		},
		{
			name:      "more than five minutes old",
			timestamp: "1614265330",
			now:       time.Unix(1614265631, 0),
			wantErr:   signing.ErrTimestampOutOfRange,
		},
		{
			name:      "more than five minutes ahead",
			timestamp: "1614265330",
			now:       time.Unix(1614265029, 0),
			wantErr:   signing.ErrTimestampOutOfRange,
		},
		{
			name:      "not an integer",
			timestamp: "1614265330.0",
			now:       vectorTimestamp,
			wantErr:   signing.ErrInvalidTimestamp,
		},
		{
			name:      "missing",
			timestamp: "",
			now:       vectorTimestamp,
			wantErr:   signing.ErrInvalidTimestamp,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := headers(vectorWebhookID, tt.timestamp, vectorSignature)
			err := signing.Verify(vectorSecret, h, []byte(vectorPayload), tt.now)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Verify = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifySignatureHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		signature string
		payload   string
		webhookID string
		secret    string
		wantErr   error
	}{
		{
			name:      "several entries, one valid",
			signature: "v1,Ceo5qEr07ixe2NLpvHk3FH9bwy/WavXrAFQ/9tdO6mc= " + vectorSignature,
			wantErr:   nil,
		},
		{
			name:      "entry without a comma before a valid one",
			signature: "v1 " + vectorSignature,
			wantErr:   nil,
		},
		{
			name:      "malformed entries before a valid one",
			signature: ",,, v1,not,base64  " + vectorSignature,
			wantErr:   nil,
		},
		{
			name:      "wrong signature",
			signature: "v1,Ceo5qEr07ixe2NLpvHk3FH9bwy/WavXrAFQ/9tdO6mc=",
			wantErr:   signing.ErrNoMatchingSignature,
		},
		{
			name:      "empty signature",
			signature: "v1,",
			wantErr:   signing.ErrNoMatchingSignature,
		},
		{
			name:      "missing header",
			signature: "",
			wantErr:   signing.ErrNoMatchingSignature,
		},
		{
			name:      "valid signature under another version",
			signature: "v2,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=",
			wantErr:   signing.ErrNoMatchingSignature,
		},
		{
			name:      "valid signature with trailing data",
			signature: vectorSignature + ",extra",
			wantErr:   signing.ErrNoMatchingSignature,
		},
		{
			name:      "payload changed",
			signature: vectorSignature,
			payload:   `{"test": 2432232315}`,
			wantErr:   signing.ErrNoMatchingSignature,
		},
		{
			name:      "webhook ID changed",
			signature: vectorSignature,
			webhookID: "msg_p5jXN8AQM9LWM0D4loKWxJel",
			wantErr:   signing.ErrNoMatchingSignature,
		},
		{
			name:      "other secret",
			signature: vectorSignature,
			secret:    "whsec_aGVyYWxkLXNlY29uZC1zZWNyZXQtMjRi",
			wantErr:   signing.ErrNoMatchingSignature,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			payload, webhookID, secret := vectorPayload, vectorWebhookID, vectorSecret
			if tt.payload != "" {
				payload = tt.payload
			}
			if tt.webhookID != "" {
				webhookID = tt.webhookID
			}
			if tt.secret != "" {
				secret = tt.secret
			}
			h := headers(webhookID, "1614265330", tt.signature)

			err := signing.Verify(secret, h, []byte(payload), vectorTimestamp)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Verify = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// FuzzVerify checks that Verify accepts a webhook only when the
// webhook-timestamp header is exactly the signed one and one of the
// space-separated entries in webhook-signature is exactly the right
// signature, and that no other header values make it panic or accept.
// Seeds are in testdata/fuzz/FuzzVerify.
func FuzzVerify(f *testing.F) {
	f.Fuzz(func(t *testing.T, signature, timestamp string) {
		h := headers(vectorWebhookID, timestamp, signature)
		err := signing.Verify(vectorSecret, h, []byte(vectorPayload), vectorTimestamp)

		valid := timestamp == "1614265330" && slices.Contains(strings.Split(signature, " "), vectorSignature)
		if valid && err != nil {
			t.Errorf("Verify(%q, %q) = %v, want nil", signature, timestamp, err)
		}
		if !valid && err == nil {
			t.Errorf("Verify(%q, %q) = nil, want an error", signature, timestamp)
		}
	})
}
