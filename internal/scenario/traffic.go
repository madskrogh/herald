package scenario

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The traffic file is the scenario's traffic as data: what the scenario
// runs, written so a person can read and edit it. A payload isn't written
// out byte by byte. The file names the Message the payload belongs to and
// its length, and the payload is a JSON object naming that Message, padded
// to that length with data made from its name (payloadData):
//
//	{"payload": {"message": "m012", "bytes": 40}}
//	{"message":"m012","data":"cbd70178c33d"}
//
// A Message that repeats another's request names the other's payload, and
// sends it byte for byte.

// ReadTraffic reads the traffic file named file in dir, and returns its
// traffic and what names the file as read. It fails on a file that holds
// anything but traffic (an unknown field, or more after it), a payload
// shorter than its frame, and traffic the scenario can't run as written
// (Scenario.check).
func ReadTraffic(dir, file string) (Scenario, TrafficFile, error) {
	path := filepath.Join(dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, TrafficFile{}, err
	}
	var s Scenario
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Scenario{}, TrafficFile{}, fmt.Errorf("%s: %w", path, err)
	}
	if dec.More() {
		return Scenario{}, TrafficFile{}, fmt.Errorf("%s: more after the traffic", path)
	}
	if err := s.check(); err != nil {
		return Scenario{}, TrafficFile{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, TrafficFile{File: file, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
}

// TrafficFile names a traffic file, as it was when a run read it.
type TrafficFile struct {
	// File is its name, in the baseline's directory.
	File string `json:"file"`
	// SHA256 is the SHA-256 of its contents, in hex.
	SHA256 string `json:"sha256"`
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
	owner := payloadMessage(m.Payload)
	p, err := payload(owner, len(m.Payload))
	if err != nil {
		return nil, fmt.Errorf("message %s: %w", m.Name, err)
	}
	if p != m.Payload {
		return nil, fmt.Errorf("message %s: payload isn't one a traffic file can hold", m.Name)
	}
	return json.Marshal(trafficMessage{
		Name: m.Name, Application: m.Application, EventType: m.EventType,
		Payload:        trafficPayload{Message: owner, Bytes: len(m.Payload)},
		IdempotencyKey: m.IdempotencyKey,
	})
}

// behaviours and faults are the values a traffic file may give them. The
// scenario runs any other value as Succeed, or as no fault.
var (
	behaviours = []Behaviour{Succeed, Fail, Slow, TimeOut, Reset, Malformed, Oversized, Redirect, TLSError, Flap}
	faults     = []Fault{"", DispatcherRestarted, NATSPaused, PostgresSlowed}
)

// check returns an error for traffic the scenario can't run as written: a
// name used twice, an unknown behaviour or fault, a Message whose
// Application or payload names something s doesn't have, or a held Endpoint
// that can't hold an Attempt when the dispatcher restarts.
func (s Scenario) check() error {
	apps := map[string]Application{}
	endpoints := map[string]Endpoint{}
	for _, app := range s.Applications {
		if _, ok := apps[app.Name]; ok {
			return fmt.Errorf("two Applications named %s", app.Name)
		}
		apps[app.Name] = app
		for _, ep := range app.Endpoints {
			if _, ok := endpoints[ep.Name]; ok {
				return fmt.Errorf("two Endpoints named %s", ep.Name)
			}
			if !slices.Contains(behaviours, ep.Behaviour) {
				return fmt.Errorf("Endpoint %s: unknown behaviour %q", ep.Name, ep.Behaviour)
			}
			endpoints[ep.Name] = ep
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
	for i, phase := range s.Phases {
		if !slices.Contains(faults, phase.Fault) {
			return fmt.Errorf("phase %d: unknown fault %q", i+1, phase.Fault)
		}
		for _, m := range phase.Messages {
			if _, ok := apps[m.Application]; !ok {
				return fmt.Errorf("message %s: no Application %q", m.Name, m.Application)
			}
			if owner := payloadMessage(m.Payload); !messages[owner] {
				return fmt.Errorf("message %s: payload of no Message %q", m.Name, owner)
			}
		}
		if err := phase.checkHeld(apps, endpoints); err != nil {
			return fmt.Errorf("phase %d: %w", i+1, err)
		}
	}
	return nil
}

// checkHeld returns an error unless the phase holds an Endpoint exactly
// when its fault is DispatcherRestarted, and that Endpoint leaves its first
// Attempt unanswered and gets a Delivery of one of the phase's Messages.
// Otherwise the restart cuts no Attempt off, or the run waits for an
// Attempt that never comes.
func (p Phase) checkHeld(apps map[string]Application, endpoints map[string]Endpoint) error {
	switch {
	case p.Fault != DispatcherRestarted && p.HeldEndpoint != "":
		return fmt.Errorf("held Endpoint with fault %q", p.Fault)
	case p.Fault != DispatcherRestarted:
		return nil
	case p.HeldEndpoint == "":
		return errors.New("dispatcher restarted with no held Endpoint")
	}
	ep, ok := endpoints[p.HeldEndpoint]
	switch {
	case !ok:
		return fmt.Errorf("no Endpoint %q to hold", p.HeldEndpoint)
	case ep.Behaviour != TimeOut:
		return fmt.Errorf("held Endpoint %s behaves as %q, not %q", ep.Name, ep.Behaviour, TimeOut)
	}
	for _, m := range p.Messages {
		if slices.ContainsFunc(apps[m.Application].Endpoints, func(e Endpoint) bool {
			return e.Name == ep.Name && matches(e.Filter, m.EventType)
		}) {
			return nil
		}
	}
	return fmt.Errorf("no Message reaches held Endpoint %s", ep.Name)
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
		return fmt.Errorf("message: %w", err)
	}
	p, err := payload(tm.Payload.Message, tm.Payload.Bytes)
	if err != nil {
		return fmt.Errorf("message %s: %w", tm.Name, err)
	}
	*m = Message{Name: tm.Name, Application: tm.Application, EventType: tm.EventType, Payload: p, IdempotencyKey: tm.IdempotencyKey}
	return nil
}

// payload returns the payload of the Message named owner, n bytes long.
func payload(owner string, n int) (string, error) {
	name, err := json.Marshal(owner)
	if err != nil {
		return "", err
	}
	start := `{"message":` + string(name) + `,"data":"`
	const end = `"}`
	frame := len(start) + len(end)
	if n < frame {
		return "", fmt.Errorf("payload of %d bytes, shorter than the %d of its frame", n, frame)
	}
	return start + payloadData(owner, n-frame) + end, nil
}

// payloadData returns n bytes of a payload's data for the Message named
// owner: the SHA-256 of "<owner> 0" in hex, then of "<owner> 1", and so on,
// cut to n. Any language can make it, and it differs from one Message to the
// next and along each payload, so a body that mixes up two payloads, or
// parts of one, doesn't pass for either.
func payloadData(owner string, n int) string {
	var b strings.Builder
	b.Grow(n + sha256.Size*2)
	for i := 0; b.Len() < n; i++ {
		sum := sha256.Sum256([]byte(owner + " " + strconv.Itoa(i)))
		b.WriteString(hex.EncodeToString(sum[:]))
	}
	return b.String()[:n]
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
