package scenario

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Compose controls the services of a compose stack, to inject faults.
type Compose struct {
	// File is the stack's compose file. Its directory's .env names the
	// compose project, so each checkout controls its own stack.
	File string
}

// stopTimeout is how long Restart lets a service shut down before killing
// it: Docker's default of 10 seconds, which Herald's services shut down
// within. Without it, docker compose restart has killed herald-dispatcher
// after about 3 seconds, before its Attempts in flight could be cut off and
// released.
const stopTimeout = "10"

// Restart stops service and starts it again. Stopping sends SIGTERM, so the
// service shuts down as it would in a rollout.
func (c Compose) Restart(ctx context.Context, service string) error {
	_, err := c.compose(ctx, "restart", "--timeout", stopTimeout, service)
	return err
}

// Pause freezes service's processes until Unpause.
func (c Compose) Pause(ctx context.Context, service string) error {
	_, err := c.compose(ctx, "pause", service)
	return err
}

// Unpause resumes service after Pause.
func (c Compose) Unpause(ctx context.Context, service string) error {
	_, err := c.compose(ctx, "unpause", service)
	return err
}

// cpuPeriod is the period, in microseconds, over which LimitCPU's quota
// applies: the kernel's default.
const cpuPeriod = 100_000

// LimitCPU limits service to the given share of a CPU, until UnlimitCPU.
// It sets a CPU quota rather than docker's --cpus, which can't be lifted
// again: --cpus 0 leaves the limit as it was.
func (c Compose) LimitCPU(ctx context.Context, service string, share float64) error {
	quota := strconv.Itoa(int(share * cpuPeriod))
	return c.update(ctx, service, "--cpu-period", strconv.Itoa(cpuPeriod), "--cpu-quota", quota)
}

// UnlimitCPU lifts LimitCPU's limit.
func (c Compose) UnlimitCPU(ctx context.Context, service string) error {
	return c.update(ctx, service, "--cpu-quota", "-1")
}

// update runs docker update with args on service's container.
func (c Compose) update(ctx context.Context, service string, args ...string) error {
	id, err := c.compose(ctx, "ps", "--quiet", service)
	if err != nil {
		return err
	}
	if id == "" {
		return fmt.Errorf("service %s has no container", service)
	}
	return runDocker(ctx, append(append([]string{"update"}, args...), id)...)
}

// compose runs docker compose with args on the stack, and returns its
// output, trimmed.
func (c Compose) compose(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "--file", c.File}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker compose %s: %w: %s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return strings.TrimSpace(string(out)), nil
}

func runDocker(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}
