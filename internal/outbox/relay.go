// Package outbox is the relay in herald-api that hands the Messages waiting in
// the outbox off to herald-dispatcher (ADR 0002).
package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
	"github.com/madskrogh/herald/internal/ingest"
)

// Defaults for the Config fields left zero.
const (
	// defaultMaxMessages bounds how many rows a round holds locked, and how
	// much a failed round has to send again.
	defaultMaxMessages = 100
	// defaultRequestTimeout stays above the 5 seconds a dispatcher takes at
	// most to store a Batch, so a slow commit isn't sent again needlessly.
	defaultRequestTimeout = 10 * time.Second
)

// How Run paces its rounds (see poll).
const (
	pollInterval = 500 * time.Millisecond
	maxBackoff   = 30 * time.Second
)

// Config configures a Relay.
type Config struct {
	// Subject is the NATS subject the dispatchers take Batches on.
	Subject string
	// MaxMessages is the most Messages a round claims and sends.
	MaxMessages int
	// RequestTimeout is how long a round waits for a dispatcher's reply. The
	// round's transaction stays open for as long.
	RequestTimeout time.Duration
}

// Relay hands off the Messages waiting in the outbox.
type Relay struct {
	store  *ingest.Store
	nc     *nats.Conn
	cfg    Config
	logger *slog.Logger
}

// NewRelay returns a Relay that claims Messages from store and sends them
// over nc.
func NewRelay(store *ingest.Store, nc *nats.Conn, cfg Config, logger *slog.Logger) *Relay {
	if cfg.MaxMessages == 0 {
		cfg.MaxMessages = defaultMaxMessages
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	return &Relay{store: store, nc: nc, cfg: cfg, logger: logger}
}

// Run hands off Messages until ctx is cancelled, one round at a time. A round
// in progress when ctx is cancelled is rolled back, and its Messages wait in
// the outbox for the next relay to run.
func (r *Relay) Run(ctx context.Context) {
	poll(ctx, func(ctx context.Context) (bool, error) {
		_, more, err := r.round(ctx, time.Now())
		return more, err
	}, pollInterval, maxBackoff, r.logger)
}

// RunOnce runs one round of the relay at the time now: it claims up to
// MaxMessages Messages from the outbox, sends as many as fit in one Batch, and
// marks them handed off once a dispatcher has committed them. It returns how
// many Messages it handed off. On an error, none are.
//
// The round is one transaction, which stays open, holding the claimed rows,
// while it waits up to RequestTimeout for the reply.
func (r *Relay) RunOnce(ctx context.Context, now time.Time) (int, error) {
	n, _, err := r.round(ctx, now)
	return n, err
}

// round is RunOnce, and also reports whether more Messages may be waiting:
// it claimed as many as it could, or sent fewer than it claimed.
func (r *Relay) round(ctx context.Context, now time.Time) (handedOff int, more bool, err error) {
	h, err := r.store.HandOff(ctx, r.cfg.MaxMessages, now, r.send)
	more = h.Claimed == r.cfg.MaxMessages || h.HandedOff < h.Claimed
	return h.HandedOff, more, err
}

// send sends as many of messages as fit, from the start, to the dispatchers
// as one Batch, and returns how many it sent. The rest stay in the outbox for
// a later round.
func (r *Relay) send(ctx context.Context, messages []ingest.OutboxMessage) (int, error) {
	// The server's maximum payload, which it told the connection.
	batch := pack(messages, int(r.nc.MaxPayload()))
	n := len(batch.GetMessages())
	if n == 0 {
		// The limits on payloads and Endpoints keep every Message well under
		// the maximum, so this means a limit was raised without the other.
		return 0, fmt.Errorf("message %s is too large for a batch of at most %d bytes", messages[0].ID, r.nc.MaxPayload())
	}
	data, err := proto.Marshal(batch)
	if err != nil {
		return 0, fmt.Errorf("encode batch: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
	defer cancel()
	resp, err := r.nc.RequestWithContext(ctx, r.cfg.Subject, data)
	if err != nil {
		return 0, fmt.Errorf("send batch: %w", err)
	}
	// Only an explicit committed = true counts (see BatchReply). A reply that
	// doesn't decode is not one.
	var reply handoffv1.BatchReply
	if err := proto.Unmarshal(resp.Data, &reply); err != nil {
		return 0, fmt.Errorf("decode batch reply: %w", err)
	}
	if !reply.GetCommitted() {
		return 0, fmt.Errorf("batch not committed: %s", reply.GetError())
	}
	return n, nil
}

// poll runs round until ctx is cancelled. A round that reports more waiting is
// followed by the next at once, to drain a backlog. Otherwise the next waits
// interval, and after a failure longer still: the wait doubles with each
// failure in a row, up to maxBackoff, so that a dispatcher outage doesn't
// turn into a loop of failing rounds.
func poll(ctx context.Context, round func(context.Context) (more bool, err error), interval, maxBackoff time.Duration, logger *slog.Logger) {
	backoff := interval
	for {
		more, err := round(ctx)
		if ctx.Err() != nil {
			return
		}
		var wait time.Duration
		switch {
		case err != nil:
			logger.WarnContext(ctx, "outbox round failed", "error", err, "retry_in", backoff)
			wait = backoff
			backoff = min(2*backoff, maxBackoff)
		case more:
			backoff = interval
			continue
		default:
			backoff = interval
			wait = interval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// pack returns a Batch of as many of messages, from the start, as fit in at
// most maxBytes. It encodes a Message only if every one before it fitted.
func pack(messages []ingest.OutboxMessage, maxBytes int) *handoffv1.Batch {
	batch := &handoffv1.Batch{}
	size := 0
	for _, m := range messages {
		msg := toProto(m)
		// A Message in a Batch is its encoding, preceded by the tag of
		// Batch.messages (field 1, one byte) and its length.
		n := proto.Size(msg)
		size += 1 + protowire.SizeVarint(uint64(n)) + n
		if size > maxBytes {
			break
		}
		batch.Messages = append(batch.Messages, msg)
	}
	return batch
}

// toProto returns m as it travels in a Batch.
func toProto(m ingest.OutboxMessage) *handoffv1.Message {
	msg := &handoffv1.Message{
		Id:        m.ID.String(),
		TenantId:  m.TenantID.String(),
		EventType: m.EventType,
		Payload:   m.Payload,
	}
	for _, d := range m.Deliveries {
		secrets := make([]*handoffv1.SigningSecret, len(d.SigningSecrets))
		for i, s := range d.SigningSecrets {
			secrets[i] = &handoffv1.SigningSecret{Ciphertext: s}
		}
		msg.Deliveries = append(msg.Deliveries, &handoffv1.Delivery{
			Id: d.ID.String(),
			Endpoint: &handoffv1.Endpoint{
				Id:             d.EndpointID.String(),
				Url:            d.URL,
				SigningSecrets: secrets,
			},
		})
	}
	return msg
}
