// Package signing signs webhooks following the Standard Webhooks spec
// (standardwebhooks.com), and verifies those signatures.
package signing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Header names defined by the spec.
const (
	headerID        = "webhook-id"
	headerTimestamp = "webhook-timestamp"
	headerSignature = "webhook-signature"
)

const secretPrefix = "whsec_"

// tolerance is how far the webhook-timestamp header may be from now, in
// either direction, for Verify to accept it. It bounds how long a captured
// webhook can be replayed.
const tolerance = 5 * time.Minute

// Errors returned by Verify.
var (
	// ErrNoMatchingSignature means no signature in the webhook-signature
	// header matches the signing secret.
	ErrNoMatchingSignature = errors.New("no matching signature")
	// ErrInvalidTimestamp means the webhook-timestamp header is missing or
	// not an integer Unix timestamp.
	ErrInvalidTimestamp = errors.New("invalid webhook-timestamp header")
	// ErrTimestampOutOfRange means the webhook-timestamp header is too far
	// from the current time.
	ErrTimestampOutOfRange = errors.New("webhook-timestamp outside tolerance")
)

// NewSecret returns a new random signing secret: "whsec_" followed by the
// base64 of 24 random bytes, the smallest key size the spec allows.
func NewSecret() string {
	key := make([]byte, 24)
	rand.Read(key) // Never returns an error; it crashes the program instead.
	return secretPrefix + base64.StdEncoding.EncodeToString(key)
}

// Sign returns the webhook-id, webhook-timestamp and webhook-signature
// headers for one Attempt. The signature header holds one signature per
// secret, space-separated, in the order of secrets.
//
// webhookID must be non-empty and contain no ".": the signed content joins
// ID, timestamp and payload with ".", so a dot in the ID would let a
// different ID and payload produce the same signature.
func Sign(webhookID string, ts time.Time, payload []byte, secrets []string) (http.Header, error) {
	if err := checkWebhookID(webhookID); err != nil {
		return nil, fmt.Errorf("sign webhook: %w", err)
	}
	if len(secrets) == 0 {
		return nil, errors.New("sign webhook: no signing secrets")
	}
	timestamp := strconv.FormatInt(ts.Unix(), 10)
	signatures := make([]string, 0, len(secrets))
	for i, secret := range secrets {
		key, err := decodeSecret(secret)
		if err != nil {
			return nil, fmt.Errorf("sign webhook: signing secret %d: %w", i, err)
		}
		signatures = append(signatures, signature(key, webhookID, timestamp, payload))
	}
	h := http.Header{}
	h.Set(headerID, webhookID)
	h.Set(headerTimestamp, timestamp)
	h.Set(headerSignature, strings.Join(signatures, " "))
	return h, nil
}

// Verify checks that the webhook-signature header in h holds a valid
// signature of payload under secret, and that the webhook-timestamp header
// is within five minutes of now. Unlike the spec's reference libraries, it
// requires the "whsec_" prefix on secret.
func Verify(secret string, h http.Header, payload []byte, now time.Time) error {
	key, err := decodeSecret(secret)
	if err != nil {
		return fmt.Errorf("verify webhook: %w", err)
	}
	webhookID := h.Get(headerID)
	if err := checkWebhookID(webhookID); err != nil {
		return fmt.Errorf("verify webhook: %w", err)
	}
	timestamp := h.Get(headerTimestamp)
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	// Only the canonical form, so that a timestamp that parses but was not
	// what Sign wrote fails here rather than as a signature mismatch.
	if err != nil || strconv.FormatInt(unix, 10) != timestamp {
		return fmt.Errorf("%w: %q", ErrInvalidTimestamp, timestamp)
	}
	if age := now.Sub(time.Unix(unix, 0)); age > tolerance || age < -tolerance {
		return fmt.Errorf("%w: %s", ErrTimestampOutOfRange, timestamp)
	}
	want := []byte(signature(key, webhookID, timestamp, payload))
	for _, entry := range strings.Split(h.Get(headerSignature), " ") {
		if hmac.Equal([]byte(entry), want) {
			return nil
		}
	}
	return ErrNoMatchingSignature
}

// signature returns one webhook-signature entry: "v1," and the base64
// HMAC-SHA256 of "<webhookID>.<timestamp>.<payload>" under key.
func signature(key []byte, webhookID, timestamp string, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(webhookID + "." + timestamp + "."))
	mac.Write(payload)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func checkWebhookID(webhookID string) error {
	if webhookID == "" || strings.Contains(webhookID, ".") {
		return fmt.Errorf("webhook ID %q is empty or contains a dot", webhookID)
	}
	return nil
}

// decodeSecret returns the HMAC key in a signing secret, which the spec
// requires to be 24 to 64 bytes. Errors never include the secret itself.
func decodeSecret(secret string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(secret, secretPrefix)
	if !ok {
		return nil, fmt.Errorf("signing secret lacks the %s prefix", secretPrefix)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode signing secret: %w", err)
	}
	if len(key) < 24 || len(key) > 64 {
		return nil, fmt.Errorf("signing secret is %d bytes, want 24 to 64", len(key))
	}
	return key, nil
}
