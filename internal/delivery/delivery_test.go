package delivery_test

import (
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/delivery"
)

func TestMessageDeliveries(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	in := newTestIntake(t.Context(), t)
	in.send(t, testBatch())

	tests := []struct {
		name      string
		messageID string
		want      []delivery.Delivery
	}{
		{"two Endpoints", messageID1, testDeliveries[:2]},
		{"one Endpoint", messageID2, testDeliveries[2:]},
		{"unknown Message", "0190a5e4-0000-7000-8000-0000000000a9", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := in.store.MessageDeliveries(t.Context(), uuid.MustParse(tt.messageID))
			if err != nil {
				t.Fatalf("MessageDeliveries: %v", err)
			}
			if diff := cmp.Diff(tt.want, got, ignoreTraceParent); diff != "" {
				t.Errorf("MessageDeliveries (-want +got):\n%s", diff)
			}
		})
	}
}
