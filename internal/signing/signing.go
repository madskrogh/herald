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

const secretPrefix = "whsec_"

// tolerance is how far the webhook-timestamp header may be from the
// receiver's clock, in either direction, for Verify to accept it. It bounds
// how long a captured webhook can be replayed against the receiver.
const tolerance = 5 * time.Minute

// Errors returned by Verify.
var (
	// ErrNoMatchingSignature means no signature in the webhook-signature
	// header matches the signing secret.
	ErrNoMatchingSignature = errors.New("no matching signature")
	// ErrInvalidTimestamp means the webhook-timestamp header is missing or
	// not an integer Unix timestamp.
	ErrInvalidTimestamp = errors.New("invalid webhook-timestamp header")
	// ErrTimestampOutOfRange means the webhook-timestamp header is more
	// than five minutes from the current time.
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
func Sign(webhookID string, ts time.Time, payload []byte, secrets []string) (http.Header, error) {
	if len(secrets) == 0 {
		return nil, errors.New("sign webhook: no signing secrets")
	}
	timestamp := strconv.FormatInt(ts.Unix(), 10)
	signatures := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		key, err := decodeSecret(secret)
		if err != nil {
			return nil, err
		}
		signatures = append(signatures, "v1,"+sign(key, webhookID, timestamp, payload))
	}
	h := http.Header{}
	h.Set("webhook-id", webhookID)
	h.Set("webhook-timestamp", timestamp)
	h.Set("webhook-signature", strings.Join(signatures, " "))
	return h, nil
}

func sign(key []byte, webhookID, timestamp string, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(webhookID + "." + timestamp + "."))
	mac.Write(payload)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Verify checks that the webhook-signature header in h holds a valid
// signature of payload under secret, and that the webhook-timestamp header
// is within five minutes of now.
func Verify(secret string, h http.Header, payload []byte, now time.Time) error {
	key, err := decodeSecret(secret)
	if err != nil {
		return err
	}
	timestamp := h.Get("webhook-timestamp")
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidTimestamp, timestamp)
	}
	if age := now.Sub(time.Unix(unix, 0)); age > tolerance || age < -tolerance {
		return fmt.Errorf("%w: %s", ErrTimestampOutOfRange, timestamp)
	}
	want := []byte("v1," + sign(key, h.Get("webhook-id"), timestamp, payload))
	for _, entry := range strings.Split(h.Get("webhook-signature"), " ") {
		if hmac.Equal([]byte(entry), want) {
			return nil
		}
	}
	return ErrNoMatchingSignature
}

// decodeSecret returns the HMAC key in a signing secret. Errors never include
// the secret itself.
func decodeSecret(secret string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(secret, secretPrefix)
	if !ok {
		return nil, fmt.Errorf("signing secret lacks the %s prefix", secretPrefix)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode signing secret: %w", err)
	}
	if len(key) == 0 {
		return nil, errors.New("signing secret is empty")
	}
	return key, nil
}
