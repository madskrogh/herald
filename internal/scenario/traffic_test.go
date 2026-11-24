package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/scenario"
)

// writeTraffic writes a traffic file holding data, and returns its path.
func writeTraffic(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "traffic.json")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadTraffic(t *testing.T) {
	t.Parallel()
	path := writeTraffic(t, `{
  "applications": [
    {"name": "app-1", "endpoints": [
      {"name": "app-1/ep-1", "behaviour": "succeed"},
      {"name": "app-1/ep-2", "filter": ["refund.*"], "behaviour": "fail"}
    ]},
    {"name": "app-2", "endpoints": [{"name": "app-2/ep-1", "behaviour": "time out"}]}
  ],
  "phases": [
    {"messages": [
      {"name": "m001", "application": "app-1", "event_type": "refund.created", "payload": {"message": "m001", "bytes": 32}, "idempotency_key": "key-m001"},
      {"name": "m002", "application": "app-1", "event_type": "refund.created", "payload": {"message": "m001", "bytes": 32}, "idempotency_key": "key-m001"}
    ]},
    {"fault": "dispatcher restarted", "held_endpoint": "app-2/ep-1", "messages": [
      {"name": "m003", "application": "app-2", "event_type": "invoice.paid", "payload": {"message": "m003", "bytes": 28}}
    ]}
  ]
}`)

	got, err := scenario.ReadTraffic(path)
	if err != nil {
		t.Fatalf("ReadTraffic: %v", err)
	}
	want := scenario.Scenario{
		Applications: []scenario.Application{
			{Name: "app-1", Endpoints: []scenario.Endpoint{
				{Name: "app-1/ep-1", Behaviour: scenario.Succeed},
				{Name: "app-1/ep-2", Filter: []string{"refund.*"}, Behaviour: scenario.Fail},
			}},
			{Name: "app-2", Endpoints: []scenario.Endpoint{{Name: "app-2/ep-1", Behaviour: scenario.TimeOut}}},
		},
		Phases: []scenario.Phase{
			{Messages: []scenario.Message{
				// 32 bytes: the 28 of the payload's frame, and 4 of data.
				{Name: "m001", Application: "app-1", EventType: "refund.created", Payload: `{"message":"m001","data":"xxxx"}`, IdempotencyKey: "key-m001"},
				// The same request as m001, payload included.
				{Name: "m002", Application: "app-1", EventType: "refund.created", Payload: `{"message":"m001","data":"xxxx"}`, IdempotencyKey: "key-m001"},
			}},
			{Fault: scenario.DispatcherRestarted, HeldEndpoint: "app-2/ep-1", Messages: []scenario.Message{
				{Name: "m003", Application: "app-2", EventType: "invoice.paid", Payload: `{"message":"m003","data":""}`},
			}},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ReadTraffic (-want +got):\n%s", diff)
	}
}

func TestReadTrafficRefuses(t *testing.T) {
	t.Parallel()
	// message returns a traffic file with one Application, app-1, and one
	// Message, m001, whose fields are given as JSON.
	message := func(fields string) string {
		return `{"applications": [{"name": "app-1", "endpoints": [{"name": "app-1/ep-1", "behaviour": "succeed"}]}],
  "phases": [{"messages": [{"name": "m001", ` + fields + `}]}]}`
	}
	tests := []struct {
		name string
		data string
		// want is part of the error.
		want string
	}{
		{
			name: "not JSON",
			data: `{"applications": [`,
			want: "unexpected EOF",
		},
		{
			name: "unknown field",
			data: message(`"application": "app-1", "event_type": "invoice.paid", "payload": {"message": "m001", "bytes": 28}, "priority": 1`),
			want: `unknown field "priority"`,
		},
		{
			name: "unknown Application",
			data: message(`"application": "app-9", "event_type": "invoice.paid", "payload": {"message": "m001", "bytes": 28}`),
			want: `m001: no Application "app-9"`,
		},
		{
			name: "payload of an unknown Message",
			data: message(`"application": "app-1", "event_type": "invoice.paid", "payload": {"message": "m009", "bytes": 28}`),
			want: `m001: payload of no Message "m009"`,
		},
		{
			name: "two Messages with one name",
			data: `{"applications": [{"name": "app-1", "endpoints": [{"name": "app-1/ep-1", "behaviour": "succeed"}]}],
  "phases": [{"messages": [
    {"name": "m001", "application": "app-1", "event_type": "invoice.paid", "payload": {"message": "m001", "bytes": 28}},
    {"name": "m001", "application": "app-1", "event_type": "invoice.paid", "payload": {"message": "m001", "bytes": 28}}
  ]}]}`,
			want: "two Messages named m001",
		},
		{
			name: "two Endpoints with one name",
			data: `{"applications": [
    {"name": "app-1", "endpoints": [{"name": "ep", "behaviour": "succeed"}]},
    {"name": "app-2", "endpoints": [{"name": "ep", "behaviour": "fail"}]}
  ],
  "phases": [{"messages": [{"name": "m001", "application": "app-1", "event_type": "invoice.paid", "payload": {"message": "m001", "bytes": 28}}]}]}`,
			want: "two Endpoints named ep",
		},
		{
			name: "payload shorter than its frame",
			data: message(`"application": "app-1", "event_type": "invoice.paid", "payload": {"message": "m001", "bytes": 27}`),
			want: "m001: payload of 27 bytes, shorter than the 28 of its frame",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := scenario.ReadTraffic(writeTraffic(t, tt.data))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ReadTraffic = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}
