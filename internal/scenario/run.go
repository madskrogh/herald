package scenario

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/madskrogh/herald/internal/e2e"
	"github.com/madskrogh/herald/internal/endpointtest"
)

const (
	// natsPause is how long NATS stays paused after a phase's Messages are
	// sent, so the relay tries to hand them off, and fails, meanwhile.
	natsPause = 5 * time.Second
	// postgresCPU is the share of a CPU Postgres gets while it is slowed:
	// little enough that each Delivery's first Attempt comes seconds late,
	// so the recording shows the fault. A tenth of a CPU barely shows.
	postgresCPU = 0.01
	// sendInterval spaces the Messages a phase sends, as traffic is spread
	// out. Sent all at once, they would build a backlog at the Endpoints
	// that accept every Event type, which goes first as the most overdue,
	// and the retries due elsewhere would wait for it by as much as seconds,
	// more in one run than another.
	sendInterval = 100 * time.Millisecond
)

// Run runs s against stack, injecting its faults through compose, and
// returns what it observed. It creates a Tenant of its own, and waits for
// each phase's Deliveries to succeed or fail before the next phase starts.
// Faults it injects are undone before it returns, whether it fails or not.
func Run(ctx context.Context, stack *e2e.Stack, compose Compose, s Scenario, logger *slog.Logger) (Observations, error) {
	obs := Observations{
		Scenario:     s,
		Sent:         map[string]Sent{},
		EndpointURLs: map[string]string{},
		EndpointIDs:  map[uuid.UUID]string{},
	}
	tenant, err := stack.NewTenant(ctx, fmt.Sprintf("scenario %d", s.Seed))
	if err != nil {
		return Observations{}, err
	}
	r := &runner{stack: stack, compose: compose, tenant: tenant, obs: &obs, logger: logger,
		appIDs: map[string]string{}, endpoints: map[string]*endpointtest.Endpoint{}}
	if err := r.create(ctx); err != nil {
		return Observations{}, err
	}
	for i, phase := range s.Phases {
		logger.InfoContext(ctx, "phase", "number", i+1, "fault", phase.Fault, "messages", len(phase.Messages))
		if err := r.phase(ctx, phase); err != nil {
			return Observations{}, fmt.Errorf("phase %d (%q): %w", i+1, phase.Fault, err)
		}
	}
	for _, ep := range r.endpoints {
		attempts, err := ep.WaitAttempts(ctx, 0)
		if err != nil {
			return Observations{}, err
		}
		obs.Attempts = append(obs.Attempts, attempts...)
	}
	return obs, nil
}

// runner runs one Scenario.
type runner struct {
	stack   *e2e.Stack
	compose Compose
	tenant  *e2e.Tenant
	obs     *Observations
	logger  *slog.Logger
	// appIDs holds the ID of each Application, by name, and endpoints the
	// fake behind each Endpoint.
	appIDs    map[string]string
	endpoints map[string]*endpointtest.Endpoint
}

// create creates the scenario's Applications and Endpoints.
func (r *runner) create(ctx context.Context) error {
	for _, app := range r.obs.Scenario.Applications {
		appID, err := r.tenant.CreateApplication(ctx, app.Name)
		if err != nil {
			return fmt.Errorf("create application %s: %w", app.Name, err)
		}
		r.appIDs[app.Name] = appID
		for _, ep := range app.Endpoints {
			var fake *endpointtest.Endpoint
			if ep.Behaviour == TLSError {
				if fake, err = r.stack.Endpoints.NewTLSEndpoint(ctx); err != nil {
					return err
				}
			} else {
				fake = r.stack.Endpoints.NewEndpoint(respond(ep.Behaviour))
			}
			id, err := r.tenant.CreateFilteredEndpoint(ctx, appID, fake, ep.Filter)
			if err != nil {
				return fmt.Errorf("create endpoint %s: %w", ep.Name, err)
			}
			uid, err := uuid.Parse(id)
			if err != nil {
				return fmt.Errorf("endpoint %s: ID %q: %w", ep.Name, id, err)
			}
			r.endpoints[ep.Name] = fake
			r.obs.EndpointURLs[fake.URL] = ep.Name
			r.obs.EndpointIDs[uid] = ep.Name
		}
	}
	return nil
}

// phase sends a phase's Messages under its fault, and waits for their
// Deliveries to finish.
func (r *runner) phase(ctx context.Context, phase Phase) (err error) {
	// Faults are undone even when ctx has ended, or the stack stays broken.
	undo := func(f func(context.Context) error) {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		err = errors.Join(err, f(uctx))
	}
	switch phase.Fault {
	case NATSPaused:
		if err := r.compose.Pause(ctx, "nats"); err != nil {
			return err
		}
		paused := true
		defer func() {
			if paused {
				undo(func(ctx context.Context) error { return r.compose.Unpause(ctx, "nats") })
			}
		}()
		if err := r.send(ctx, phase.Messages); err != nil {
			return err
		}
		if err := sleep(ctx, natsPause); err != nil {
			return err
		}
		paused = false
		if err := r.compose.Unpause(ctx, "nats"); err != nil {
			return err
		}
	case PostgresSlowed:
		if err := r.compose.LimitCPU(ctx, "postgres", postgresCPU); err != nil {
			return err
		}
		defer undo(func(ctx context.Context) error { return r.compose.UnlimitCPU(ctx, "postgres") })
		if err := r.send(ctx, phase.Messages); err != nil {
			return err
		}
	case DispatcherRestarted:
		if err := r.send(ctx, phase.Messages); err != nil {
			return err
		}
		held, ok := r.endpoints[phase.HeldEndpoint]
		if !ok {
			return fmt.Errorf("no endpoint %q to hold an Attempt", phase.HeldEndpoint)
		}
		if _, err := held.WaitAttempts(ctx, 1); err != nil {
			return err
		}
		if err := r.compose.Restart(ctx, "herald-dispatcher"); err != nil {
			return err
		}
	default:
		if err := r.send(ctx, phase.Messages); err != nil {
			return err
		}
	}
	return r.wait(ctx, phase.Messages)
}

// send sends messages in order, sendInterval apart, and keeps what the
// Tenant API answered.
func (r *runner) send(ctx context.Context, messages []Message) error {
	for i, m := range messages {
		if i > 0 {
			if err := sleep(ctx, sendInterval); err != nil {
				return err
			}
		}
		at := time.Now()
		id, err := r.tenant.SendMessage(ctx, r.appIDs[m.Application], m.EventType, m.Payload, m.IdempotencyKey)
		var refused *e2e.StatusError
		switch {
		case errors.As(err, &refused):
			r.obs.Sent[m.Name] = Sent{At: at, Status: refused.Code}
			continue
		case err != nil:
			return fmt.Errorf("send message %s: %w", m.Name, err)
		}
		uid, err := uuid.Parse(id)
		if err != nil {
			return fmt.Errorf("message %s: ID %q: %w", m.Name, id, err)
		}
		r.obs.Sent[m.Name] = Sent{At: at, ID: uid}
	}
	return nil
}

// wait waits until the Deliveries of messages have all succeeded or failed,
// and keeps them.
func (r *runner) wait(ctx context.Context, messages []Message) error {
	waited := map[uuid.UUID]bool{}
	for _, m := range messages {
		sent := r.obs.Sent[m.Name]
		if sent.Status != 0 || waited[sent.ID] {
			continue
		}
		waited[sent.ID] = true
		deliveries, err := r.stack.WaitDeliveries(ctx, sent.ID.String())
		if err != nil {
			return fmt.Errorf("message %s: %w", m.Name, err)
		}
		r.obs.Deliveries = append(r.obs.Deliveries, deliveries...)
	}
	return nil
}

// sleep waits for d, or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
