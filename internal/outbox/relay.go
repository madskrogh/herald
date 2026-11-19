// Package outbox is the relay in herald-api that hands the Messages waiting in
// the outbox off to herald-dispatcher (ADR 0002).
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	handoffv1 "github.com/madskrogh/herald/internal/gen/herald/handoff/v1"
	"github.com/madskrogh/herald/internal/ingest"
	"github.com/madskrogh/herald/internal/telemetry"
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
	// RequestTimeout is how long a round waits for a dispatcher's reply to
	// each Batch it sends. The round's transaction stays open for as long.
	RequestTimeout time.Duration
	// TracerProvider makes the spans of each Batch and of each Message in it,
	// or nil for none.
	TracerProvider trace.TracerProvider
}

// tracerName names the tracer of the relay's spans.
const tracerName = "github.com/madskrogh/herald/internal/outbox"

// Relay hands off the Messages waiting in the outbox.
type Relay struct {
	store  *ingest.Store
	nc     *nats.Conn
	cfg    Config
	tracer trace.Tracer
	logger *slog.Logger

	// mu makes the Relay's rounds take turns, and guards claimLimit.
	mu sync.Mutex
	// claimLimit is how many Messages the next round claims: MaxMessages,
	// or fewer after rounds that couldn't send all they claimed.
	//
	// Every Message a round claims is read and held locked until the reply,
	// so claiming many more than fit in a Batch would read their payloads for
	// nothing, and keep other relays from them. After a round that sent fewer
	// than it claimed, the next claims one more than it sent, to see whether
	// that one fits too. After one that sent all it claimed, the next claims
	// twice as many, up to MaxMessages.
	claimLimit int
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
	if cfg.TracerProvider == nil {
		cfg.TracerProvider = noop.NewTracerProvider()
	}
	return &Relay{store: store, nc: nc, cfg: cfg, tracer: cfg.TracerProvider.Tracer(tracerName), logger: logger, claimLimit: cfg.MaxMessages}
}

// Run hands off Messages until ctx is cancelled, one round at a time. A round
// in progress when ctx is cancelled is rolled back, and its Messages wait in
// the outbox for the next relay to run.
func (r *Relay) Run(ctx context.Context) {
	poll(ctx, func(ctx context.Context) (bool, error) {
		_, more, err := r.RunOnce(ctx, time.Now())
		return more, err
	}, pollInterval, maxBackoff, r.logger)
}

// RunOnce runs one round of the relay at the time now: it claims up to
// MaxMessages Messages from the outbox (fewer after rounds whose Messages
// didn't all fit in a Batch), sends as many as fit in one Batch, and marks
// them handed off once a dispatcher has committed them. A Message that a
// dispatcher rejects on its own is parked instead (see send), and logged. It
// returns how many Messages it handed off, and whether more may be waiting: it
// claimed as many as it asked for, or sent or parked fewer than it claimed. On
// an error, none are handed off or parked.
//
// A round that claims fewer than it asked for reports nothing more waiting,
// even when other relays' rounds hold the rest locked. Those Messages are
// being handed off already, and claiming again at once would only find them
// still locked.
//
// The round is one transaction, which stays open, holding the claimed rows,
// while it waits up to RequestTimeout for each reply. A Batch that is not
// committed is sent again in parts, so a round can wait for several.
func (r *Relay) RunOnce(ctx context.Context, now time.Time) (handedOff int, more bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Without a connection there is no one to send to, and no maximum payload
	// to pack against, so don't claim rows. A connection that drops after
	// this check fails the request, which rolls the round back.
	if !r.nc.IsConnected() {
		return 0, false, fmt.Errorf("not connected to NATS (%s)", r.nc.Status())
	}
	var spans roundSpans
	h, err := r.store.HandOff(ctx, r.claimLimit, now, func(ctx context.Context, messages []ingest.OutboxMessage) (ingest.SendResult, error) {
		sent, err := r.send(ctx, messages, &spans)
		spans.sent = sent
		return sent, err
	})
	// The outcome of each Message is known only once the round's
	// transaction has ended.
	spans.end(err)
	// One record per parked Message, so that they can be found and counted.
	for _, p := range h.Parked {
		r.logger.WarnContext(ctx, "message parked", "message_id", p.ID, "error", p.Reason)
	}
	// Parked Messages are done with, as handed off ones are.
	sent := h.HandedOff + len(h.Parked)
	more = h.Claimed == r.claimLimit || sent < h.Claimed
	// Size the next claim (see claimLimit).
	switch {
	case err != nil:
		// A failed round says nothing about how many fit.
	case sent < h.Claimed:
		r.claimLimit = sent + 1
	case h.Claimed == r.claimLimit:
		r.claimLimit = min(2*r.claimLimit, r.cfg.MaxMessages)
	}
	return h.HandedOff, more, err
}

// send sends as many of messages as fit, from the start, to the dispatchers
// as one Batch. The rest stay in the outbox for a later round. It starts the
// spans of the Batch and of each Message in it in spans.
//
// If the dispatcher doesn't commit the Batch, send looks for the Message that
// makes it fail: it sends each half of the Batch on its own, and each half
// that fails in turn, until it has the Messages that fail alone. Those are
// parked, but only if a dispatcher committed some other Message of the Batch.
// A dispatcher that commits none may be failing as a whole (its database is
// down, say), and then no Message is to blame: the round fails and the
// Messages wait in the outbox.
func (r *Relay) send(ctx context.Context, messages []ingest.OutboxMessage, spans *roundSpans) (ingest.SendResult, error) {
	// The server's maximum payload, which it told the connection.
	batch := pack(messages, int(r.nc.MaxPayload()))
	n := len(batch.GetMessages())
	if n == 0 {
		// The limits on payloads and Endpoints keep every Message well under
		// the maximum, so this means a limit was raised without the other.
		// No dispatcher could take it, whatever state it is in, so it is
		// parked without sending. The rest wait for the next round.
		reason := fmt.Sprintf("message is too large for a batch of at most %d bytes", r.nc.MaxPayload())
		spans.startHandoff(ctx, r.tracer, messages[0], 0)
		return ingest.SendResult{Parked: []ingest.ParkedMessage{{ID: messages[0].ID, Reason: reason}}}, nil
	}
	spans.start(ctx, r.tracer, messages[:n], batch.GetMessages())
	committed, reason, err := r.request(ctx, batch.GetMessages())
	if err != nil {
		return ingest.SendResult{}, err
	}
	if committed {
		return ingest.SendResult{HandedOff: messageIDs(messages[:n])}, nil
	}
	var sent ingest.SendResult
	if err := r.bisect(ctx, messages[:n], batch.GetMessages(), reason, &sent); err != nil {
		return ingest.SendResult{}, err
	}
	if len(sent.HandedOff) == 0 {
		return ingest.SendResult{}, fmt.Errorf("batch not committed: %s", reason)
	}
	return sent, nil
}

// bisect finds which of messages a dispatcher rejects on their own. It was
// told, with reason, that it didn't commit them together; encoded holds the
// same Messages as they are sent. It adds the Messages a dispatcher commits
// to sent.HandedOff, and those that fail alone to sent.Parked.
func (r *Relay) bisect(ctx context.Context, messages []ingest.OutboxMessage, encoded []*handoffv1.Message, reason string, sent *ingest.SendResult) error {
	if len(messages) == 1 {
		sent.Parked = append(sent.Parked, ingest.ParkedMessage{ID: messages[0].ID, Reason: reason})
		return nil
	}
	mid := len(messages) / 2
	for _, half := range []struct {
		messages []ingest.OutboxMessage
		encoded  []*handoffv1.Message
	}{
		{messages[:mid], encoded[:mid]},
		{messages[mid:], encoded[mid:]},
	} {
		committed, reason, err := r.request(ctx, half.encoded)
		if err != nil {
			return err
		}
		if committed {
			sent.HandedOff = append(sent.HandedOff, messageIDs(half.messages)...)
			continue
		}
		if err := r.bisect(ctx, half.messages, half.encoded, reason, sent); err != nil {
			return err
		}
	}
	return nil
}

// request sends messages to the dispatchers as one Batch and reports whether
// a dispatcher committed it, and if not, why. A reply that doesn't say so, or
// none within RequestTimeout, is an error: it says nothing about the Messages.
func (r *Relay) request(ctx context.Context, messages []*handoffv1.Message) (committed bool, reason string, err error) {
	data, err := proto.Marshal(&handoffv1.Batch{Messages: messages})
	if err != nil {
		return false, "", fmt.Errorf("encode batch: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
	defer cancel()
	resp, err := r.nc.RequestWithContext(ctx, r.cfg.Subject, data)
	if err != nil {
		return false, "", fmt.Errorf("send batch: %w", err)
	}
	// Only an explicit committed = true counts (see BatchReply). A reply that
	// doesn't decode is not one.
	var reply handoffv1.BatchReply
	if err := proto.Unmarshal(resp.Data, &reply); err != nil {
		return false, "", fmt.Errorf("decode batch reply: %w", err)
	}
	return reply.GetCommitted(), reply.GetError(), nil
}

func messageIDs(messages []ingest.OutboxMessage) []uuid.UUID {
	ids := make([]uuid.UUID, len(messages))
	for i, m := range messages {
		ids[i] = m.ID
	}
	return ids
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

// traceParentSize is how many bytes a Message's traceparent adds to its
// encoding: the field's tag and length, one byte each, and a W3C
// traceparent, which is 55 bytes in its version 00.
const traceParentSize = 1 + 1 + 55

// pack returns a Batch of as many of messages, from the start, as fit in at
// most maxBytes. It encodes a Message only if every one before it fitted.
// Each Message's traceparent is set only once it is packed, when its span
// starts, so room is left for it.
func pack(messages []ingest.OutboxMessage, maxBytes int) *handoffv1.Batch {
	batch := &handoffv1.Batch{}
	size := 0
	for _, m := range messages {
		msg := toProto(m)
		// A Message in a Batch is its encoding, preceded by the tag of
		// Batch.messages (field 1, one byte) and its length.
		n := proto.Size(msg) + traceParentSize
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
		Id:            m.ID.String(),
		TenantId:      m.TenantID.String(),
		ApplicationId: m.ApplicationID.String(),
		EventType:     m.EventType,
		Payload:       m.Payload,
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

// roundSpans holds the spans of one round's Batch, and of each Message in
// it, which end once the round's transaction has.
type roundSpans struct {
	// batch is the Batch's span, or nil if the round sent none.
	batch trace.Span
	// handoffs holds the span of each Message the round sent or parked.
	handoffs map[uuid.UUID]trace.Span
	// sent is what the round's send did with its Messages.
	sent ingest.SendResult
}

// start starts the span of the Batch holding messages, a trace of its own,
// and the span of each Message in it, in the Message's trace. encoded holds
// the same Messages as they are sent: each gets its span's context, so the
// dispatcher continues the Message's trace.
func (rs *roundSpans) start(ctx context.Context, tracer trace.Tracer, messages []ingest.OutboxMessage, encoded []*handoffv1.Message) {
	_, rs.batch = tracer.Start(ctx, telemetry.SpanHandoffBatch,
		trace.WithNewRoot(),
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(telemetry.BatchSize.Int(len(messages))),
	)
	for i, m := range messages {
		spanCtx, span := rs.startHandoff(ctx, tracer, m, len(messages))
		encoded[i].Traceparent = telemetry.TraceParent(spanCtx)
		rs.batch.AddLink(trace.Link{SpanContext: span.SpanContext()})
	}
}

// startHandoff starts the span of handing off m in a Batch of batchSize
// Messages, as a child of the span m was accepted under. A Message stored
// without trace context starts a trace of its own.
func (rs *roundSpans) startHandoff(ctx context.Context, tracer trace.Tracer, m ingest.OutboxMessage, batchSize int) (context.Context, trace.Span) {
	ctx, span := telemetry.Start(ctx, tracer, m.TraceParent, telemetry.SpanHandoff,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			telemetry.TenantID.String(m.TenantID.String()),
			telemetry.ApplicationID.String(m.ApplicationID.String()),
			telemetry.MessageID.String(m.ID.String()),
			telemetry.BatchSize.Int(batchSize),
			telemetry.DeliveryCount.Int(len(m.Deliveries)),
		),
	)
	if rs.handoffs == nil {
		rs.handoffs = map[uuid.UUID]trace.Span{}
	}
	rs.handoffs[m.ID] = span
	return ctx, span
}

// end ends the spans, with the outcome of a round that ended with err. When
// the round fails, none of its Messages is handed off or parked.
func (rs *roundSpans) end(err error) {
	if err != nil {
		for _, span := range rs.handoffs {
			telemetry.End(span, telemetry.OutcomeFailed, err)
		}
	} else {
		for _, id := range rs.sent.HandedOff {
			telemetry.End(rs.handoffs[id], telemetry.OutcomeHandedOff, nil)
		}
		for _, p := range rs.sent.Parked {
			telemetry.End(rs.handoffs[p.ID], telemetry.OutcomeParked, errors.New(p.Reason))
		}
	}
	if rs.batch != nil {
		outcome := telemetry.OutcomeHandedOff
		if err != nil {
			outcome = telemetry.OutcomeFailed
		}
		telemetry.End(rs.batch, outcome, err)
	}
}
