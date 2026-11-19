// Package demoendpoint serves an Endpoint for trying Herald out locally: it
// prints each Attempt it receives, and whether its signature verifies.
package demoendpoint

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/madskrogh/herald/internal/signing"
)

// Handler prints each Attempt to out: when it arrived, its webhook-id,
// whether its signature verifies under secret, and its body. It answers 200
// OK, or 401 Unauthorized when the signature doesn't verify, as a real
// Endpoint would, so Herald retries the Attempt.
func Handler(out io.Writer, secret string, now func() time.Time) http.Handler {
	// Attempts arrive concurrently; each is printed whole.
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received := now()
		status, verdict := http.StatusOK, "signature valid"
		if secret == "" {
			verdict = "signature not checked: no signing secret given"
		} else if err := signing.Verify(secret, r.Header, body, received); err != nil {
			status, verdict = http.StatusUnauthorized, "signature invalid: "+err.Error()
		}
		mu.Lock()
		fmt.Fprintf(out, "%s %s %s webhook-id %s: %s\n%s\n\n",
			received.Format("15:04:05.000"), r.Method, r.URL.Path, r.Header.Get("webhook-id"), verdict, body)
		mu.Unlock()
		w.WriteHeader(status)
	})
}
