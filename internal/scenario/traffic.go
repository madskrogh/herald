package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// The traffic file is the scenario's traffic as data: what the scenario
// runs, written so a person can read and edit it. A payload isn't written
// out byte by byte. The file names the Message the payload belongs to and
// its length, and the payload is a JSON object naming that Message, padded
// with x's to that length:
//
//	{"payload": {"message": "m012", "bytes": 40}}
//	{"message":"m012","data":"xxxxxxxxxxxx"}
//
// A Message that repeats another's request names the other's payload, and
// sends it byte for byte.

// ReadTraffic reads the traffic file at path. It fails on a file that holds
// anything but traffic: an unknown field, a Message for an Application the
// file doesn't have, or a payload of a Message it doesn't have, or shorter
// than its frame.
func ReadTraffic(path string) (Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	var s Scenario
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Scenario{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := s.check(); err != nil {
		return Scenario{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// WriteTraffic writes s to w as a traffic file.
// It fails on a payload the file can't write: one ReadTraffic wouldn't read
// back byte for byte.
func WriteTraffic(w io.Writer, s Scenario) error {
	enc := json.NewEncoder(w)
	// Filters such as "refund.>" read as written.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// MarshalJSON writes m as the traffic file writes it.
func (m Message) MarshalJSON() ([]byte, error) {
	of := payloadMessage(m.Payload)
	if p, err := payload(of, len(m.Payload)); err != nil || p != m.Payload {
		return nil, fmt.Errorf("message %s: payload isn't one a traffic file can hold", m.Name)
	}
	return json.Marshal(trafficMessage{
		Name: m.Name, Application: m.Application, EventType: m.EventType,
		Payload:        trafficPayload{Message: of, Bytes: len(m.Payload)},
		IdempotencyKey: m.IdempotencyKey,
	})
}

// check returns an error for a Message whose Application or payload names
// something s doesn't have.
func (s Scenario) check() error {
	apps := map[string]bool{}
	endpoints := map[string]bool{}
	for _, app := range s.Applications {
		apps[app.Name] = true
		for _, ep := range app.Endpoints {
			if endpoints[ep.Name] {
				return fmt.Errorf("two Endpoints named %s", ep.Name)
			}
			endpoints[ep.Name] = true
		}
	}
	messages := map[string]bool{}
	for _, phase := range s.Phases {
		for _, m := range phase.Messages {
			if messages[m.Name] {
				return fmt.Errorf("two Messages named %s", m.Name)
			}
			messages[m.Name] = true
		}
	}
	for _, phase := range s.Phases {
		for _, m := range phase.Messages {
			if !apps[m.Application] {
				return fmt.Errorf("%s: no Application %q", m.Name, m.Application)
			}
			if of := payloadMessage(m.Payload); !messages[of] {
				return fmt.Errorf("%s: payload of no Message %q", m.Name, of)
			}
		}
	}
	return nil
}

// trafficPayload is how the traffic file writes a payload.
type trafficPayload struct {
	Message string `json:"message"`
	Bytes   int    `json:"bytes"`
}

// trafficMessage is how the traffic file writes a Message.
type trafficMessage struct {
	Name           string         `json:"name"`
	Application    string         `json:"application"`
	EventType      string         `json:"event_type"`
	Payload        trafficPayload `json:"payload"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}

// UnmarshalJSON reads m as the traffic file writes it.
func (m *Message) UnmarshalJSON(data []byte) error {
	var tm trafficMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tm); err != nil {
		return fmt.Errorf("message %s: %w", tm.Name, err)
	}
	p, err := payload(tm.Payload.Message, tm.Payload.Bytes)
	if err != nil {
		return fmt.Errorf("%s: %w", tm.Name, err)
	}
	*m = Message{Name: tm.Name, Application: tm.Application, EventType: tm.EventType, Payload: p, IdempotencyKey: tm.IdempotencyKey}
	return nil
}

// payload returns the payload of the Message named of, n bytes long.
func payload(of string, n int) (string, error) {
	name, err := json.Marshal(of)
	if err != nil {
		return "", err
	}
	start := `{"message":` + string(name) + `,"data":"`
	const end = `"}`
	frame := len(start) + len(end)
	if n < frame {
		return "", fmt.Errorf("payload of %d bytes, shorter than the %d of its frame", n, frame)
	}
	return start + strings.Repeat("x", n-frame) + end, nil
}

// payloadMessage returns the name of the Message that payload p belongs to,
// or "" if it names none.
func payloadMessage(p string) string {
	var named struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(p), &named) != nil {
		return ""
	}
	return named.Message
}
