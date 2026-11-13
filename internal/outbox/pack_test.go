package outbox

import (
	"bytes"
	"testing"

	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
)

func TestPackFitsWholeMessagesUnderTheCap(t *testing.T) {
	t.Parallel()

	// Each Message encodes to 1041 bytes: its ID takes 1 + 1 + 36, and its
	// payload 1 + 2 + 1000. In a Batch it takes 1 + 2 + 1041 = 1044.
	message := func() *handoffv1.Message {
		return &handoffv1.Message{
			Id:      "0190a5e4-0000-7000-8000-0000000000a1",
			Payload: bytes.Repeat([]byte("x"), 1000),
		}
	}
	three := []*handoffv1.Message{message(), message(), message()}

	tests := []struct {
		name     string
		messages []*handoffv1.Message
		maxBytes int
		want     int
	}{
		{name: "all fit", messages: three, maxBytes: 3 * 1044, want: 3},
		{name: "two fit exactly", messages: three, maxBytes: 2 * 1044, want: 2},
		{name: "a byte short of two", messages: three, maxBytes: 2*1044 - 1, want: 1},
		{name: "the first doesn't fit", messages: three, maxBytes: 1043, want: 0},
		{name: "no Messages", maxBytes: 1 << 20, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := pack(tt.messages, tt.maxBytes); got != tt.want {
				t.Errorf("pack(%d Messages, %d) = %d, want %d", len(tt.messages), tt.maxBytes, got, tt.want)
			}
		})
	}
}
