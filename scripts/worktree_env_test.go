// Package scripts holds the tests of the shell scripts in this directory.
package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo is a temporary git repository with worktrees, and a fake docker
// whose `docker ps` prints what the test sets.
type repo struct {
	t      *testing.T
	main   string
	bin    string
	script string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	script, err := filepath.Abs("worktree-env.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r := &repo{t: t, main: filepath.Join(dir, "main"), bin: filepath.Join(dir, "bin"), script: script}
	if err := os.Mkdir(r.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	r.dockerPS("")
	r.git(dir, "init", "-q", "main")
	r.git(r.main, "-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init")
	return r
}

func (r *repo) git(dir string, args ...string) {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = r.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// addWorktree adds a worktree named name and returns its directory.
func (r *repo) addWorktree(name string) string {
	r.t.Helper()
	dir := filepath.Join(r.main, ".claude", "worktrees", name)
	r.git(r.main, "worktree", "add", "-q", "-b", "worktree-"+name, dir)
	return dir
}

// dockerPS makes `docker ps` print ports, one container's Ports per line.
func (r *repo) dockerPS(ports string) {
	r.t.Helper()
	out := filepath.Join(r.bin, "ps-output")
	if err := os.WriteFile(out, []byte(ports), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.fakeDocker("#!/bin/sh\ncat '" + out + "'\n")
}

// dockerFails makes every docker command fail, as when Docker isn't running.
func (r *repo) dockerFails() {
	r.t.Helper()
	r.fakeDocker("#!/bin/sh\necho 'Cannot connect to the Docker daemon' >&2\nexit 1\n")
}

func (r *repo) fakeDocker(src string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.bin, "docker"), []byte(src), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

// run runs worktree-env.sh in dir and returns its error and output.
func (r *repo) run(dir string) (string, error) {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "bash", r.script)
	cmd.Dir = dir
	cmd.Env = r.env()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// env is the environment git and the script run in: the fake docker first on
// PATH, and no git configuration or GIT_* variables from outside the test,
// which could point git at another repository or run hooks.
func (r *repo) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") && !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	return append(env,
		"PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

// slot runs worktree-env.sh in dir and returns the slot it wrote to .env.
func (r *repo) slot(dir string) string {
	r.t.Helper()
	if out, err := r.run(dir); err != nil {
		r.t.Fatalf("worktree-env.sh in %s: %v\n%s", dir, err, out)
	}
	return envValue(r.t, dir, "HERALD_SLOT")
}

func envValue(t *testing.T, dir, key string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(b)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			return v
		}
	}
	t.Fatalf("%s/.env has no %s", dir, key)
	return ""
}

func TestWorktreeSkipsSlotWhosePortsArePublished(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		ports string
		want  string
	}{
		{
			// A removed worktree's stack in slot 1: its Postgres port is 5532.
			name:  "postgres port of slot 1",
			ports: "127.0.0.1:5532->5432/tcp\n",
			want:  "2",
		},
		{
			// Only the dispatcher's pprof port of slot 1 (6161), on IPv6.
			name:  "one other port of slot 1",
			ports: "[::]:6161->6060/tcp\n",
			want:  "2",
		},
		{
			name:  "slots 1 and 2 on one line each",
			ports: "127.0.0.1:6160->6060/tcp, 127.0.0.1:8180->8080/tcp\n0.0.0.0:4422->4222/tcp, [::]:4422->4222/tcp\n",
			want:  "3",
		},
		{
			// 5433 is no slot's port: slot 1's Postgres is 5532.
			name:  "ports of no slot",
			ports: "127.0.0.1:5433->5432/tcp, 127.0.0.1:3000->3000/tcp\n",
			want:  "1",
		},
		{
			name:  "a published range covering slot 1's NATS port",
			ports: "0.0.0.0:4300-4400->4300-4400/tcp\n",
			want:  "2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRepo(t)
			r.dockerPS(tt.ports)
			wt := r.addWorktree("new")
			if got := r.slot(wt); got != tt.want {
				t.Errorf("slot = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestWorktreeSkipsSlotInAnotherWorktreesEnv(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	if got := r.slot(r.addWorktree("first")); got != "1" {
		t.Fatalf("first worktree's slot = %s, want 1", got)
	}
	if got := r.slot(r.addWorktree("second")); got != "2" {
		t.Errorf("second worktree's slot = %s, want 2", got)
	}
}

// A worktree whose directory was deleted is still listed by git until
// pruned, and holds no slot.
func TestWorktreeTakesSlotOfDeletedWorktree(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	gone := r.addWorktree("gone")
	r.slot(gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if got := r.slot(r.addWorktree("new")); got != "1" {
		t.Errorf("slot = %s, want 1", got)
	}
}

// Regenerating .env, as make does when the script changes, keeps its slot
// and needs no Docker.
func TestExistingEnvKeepsSlotWithoutDocker(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.dockerPS("127.0.0.1:5532->5432/tcp\n")
	wt := r.addWorktree("new")
	if got := r.slot(wt); got != "2" {
		t.Fatalf("slot = %s, want 2", got)
	}
	r.dockerFails()
	if got := r.slot(wt); got != "2" {
		t.Errorf("slot after regenerating = %s, want 2", got)
	}
}

func TestNewSlotFails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// arrange sets up the repo so that picking a slot for a new
		// worktree must fail.
		arrange func(t *testing.T, r *repo)
		wantOut string
	}{
		{
			name:    "when Docker can't be asked",
			arrange: func(_ *testing.T, r *repo) { r.dockerFails() },
			wantOut: "is Docker running?",
		},
		{
			// The other worktree's slot can't be seen.
			name: "when another worktree can't be read",
			arrange: func(t *testing.T, r *repo) {
				if os.Geteuid() == 0 {
					t.Skip("root reads a directory whatever its mode")
				}
				other := r.addWorktree("other")
				r.slot(other)
				if err := os.Chmod(other, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(other, 0o755); err != nil {
						t.Error(err)
					}
				})
			},
			wantOut: "can't read",
		},
		{
			name: "when another worktree's .env can't be read",
			arrange: func(t *testing.T, r *repo) {
				if os.Geteuid() == 0 {
					t.Skip("root reads a file whatever its mode")
				}
				other := r.addWorktree("other")
				r.slot(other)
				if err := os.Chmod(filepath.Join(other, ".env"), 0); err != nil {
					t.Fatal(err)
				}
			},
			wantOut: "can't read",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRepo(t)
			tt.arrange(t, r)
			wt := r.addWorktree("new")
			out, err := r.run(wt)
			if err == nil {
				t.Fatalf("worktree-env.sh succeeded, want it to fail:\n%s", out)
			}
			if !strings.Contains(out, tt.wantOut) {
				t.Errorf("output doesn't contain %q:\n%s", tt.wantOut, out)
			}
			if _, err := os.Stat(filepath.Join(wt, ".env")); !os.IsNotExist(err) {
				t.Errorf("stat .env: %v, want it not to exist", err)
			}
		})
	}
}
