package demoendpoint_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/demoendpoint"
)

// The test vector published with the Standard Webhooks reference libraries.
const (
	vectorSecret    = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	vectorWebhookID = "msg_p5jXN8AQM9LWM0D4loKWxJek"
	vectorPayload   = `{"test": 2432232314}`
	vectorSignature = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
)

// vectorTime is the vector's webhook-timestamp, as the time it arrives.
var vectorTime = time.Unix(1614265330, 0).UTC()

func TestHandlerPrintsEachAttempt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		secret string
		want   string
	}{
		{
			name:   "valid signature",
			secret: vectorSecret,
			want: "15:02:10.000 POST /hooks webhook-id msg_p5jXN8AQM9LWM0D4loKWxJek: signature valid\n" +
				`{"test": 2432232314}` + "\n\n",
		},
		{
			name: "another Endpoint's secret",
			// A valid secret, but not the one the vector was signed with.
			secret: "whsec_dGhpcyBpcyBub3QgdGhlIHZlY3RvciBzZWNyZXQ=",
			want: "15:02:10.000 POST /hooks webhook-id msg_p5jXN8AQM9LWM0D4loKWxJek: signature invalid: no matching signature\n" +
				`{"test": 2432232314}` + "\n\n",
		},
		{
			name:   "no secret",
			secret: "",
			want: "15:02:10.000 POST /hooks webhook-id msg_p5jXN8AQM9LWM0D4loKWxJek: signature not checked: no signing secret given\n" +
				`{"test": 2432232314}` + "\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			h := demoendpoint.Handler(&out, tt.secret, func() time.Time { return vectorTime })

			req := httptest.NewRequest(http.MethodPost, "/hooks", strings.NewReader(vectorPayload))
			req.Header.Set("webhook-id", vectorWebhookID)
			req.Header.Set("webhook-timestamp", "1614265330")
			req.Header.Set("webhook-signature", vectorSignature)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if diff := cmp.Diff(tt.want, out.String()); diff != "" {
				t.Errorf("printed (-want +got):\n%s", diff)
			}
		})
	}
}
