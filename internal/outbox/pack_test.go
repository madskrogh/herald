package outbox

import (
	"bytes"
	"testing"
	"uuid"

	"github.com/madskrogh/herald/internal/ingest"
)

func TestPackFitsWholeMessagesUnderTheCap(t *testing.T) {
	t.Parallel()

	// Each Message encodes to 1079 bytes: its ID and its Tenant's ID take
	// 1 + 1 + 36 each, and its payload 1 + 2 + 1000. In a Batch it takes
	// 1 + 2 + 1079 = 1082.
	message := func() ingest.OutboxMessage {
		return ingest.OutboxMessage{
			ID:       uuid.MustParse("0190a5e4-0000-7000-8000-0000000000a1"),
			TenantID: uuid.MustParse("0190a5e4-0000-7000-8000-000000000001"),
			Payload:  bytes.Repeat([]byte("x"), 1000),
		}
	}
	three := []ingest.OutboxMessage{message(), message(), message()}

	tests := []struct {
		name     string
		messages []ingest.OutboxMessage
		maxBytes int
		want     int
	}{
		{name: "all fit", messages: three, maxBytes: 3 * 1082, want: 3},
		{name: "two fit exactly", messages: three, maxBytes: 2 * 1082, want: 2},
		{name: "a byte short of two", messages: three, maxBytes: 2*1082 - 1, want: 1},
		{name: "the first doesn't fit", messages: three, maxBytes: 1081, want: 0},
		{name: "no Messages", maxBytes: 1 << 20, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			batch := pack(tt.messages, tt.maxBytes)
			if got := len(batch.GetMessages()); got != tt.want {
				t.Errorf("pack(%d Messages, %d) packed %d, want %d", len(tt.messages), tt.maxBytes, got, tt.want)
			}
		})
	}
}
