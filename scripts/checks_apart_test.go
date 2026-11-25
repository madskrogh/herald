package scripts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestChecksApart runs checks-apart.sh on a branch whose commits change
// files, and checks whether it fails and which files it names.
func TestChecksApart(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// existing are committed on main before the branch starts, for a
		// change to modify, rename or delete.
		existing []string
		// change runs in the branch's checkout, before its commit.
		change func(t *testing.T, dir string)
		fails  bool
		// output holds what the output must hold, each in order.
		output []string
	}{
		{
			name: "a check file and another file",
			change: func(t *testing.T, dir string) {
				write(t, dir, "internal/scenario/margins.go", "internal/delivery/worker.go")
			},
			fails: true,
			output: []string{
				"check files:\n  internal/scenario/margins.go\n",
				"other files:\n  internal/delivery/worker.go\n",
			},
		},
		{
			name: "every kind of check file, and only those",
			change: func(t *testing.T, dir string) {
				write(t, dir,
					"internal/scenario/edge.go",
					"internal/scenario/testdata/report-pass.golden",
					"internal/e2e/run_spans.sql",
					"internal/e2e/postgres/queries.sql",
					"internal/e2e/e2edb/queries.sql.go",
					"cmd/scenario-timing/main.go",
					"scripts/scenario-timing.sh",
					"scripts/profile.sh",
					"scripts/checks-apart.sh",
					"scripts/checks_apart_test.go",
					"docs/testing.md",
					".github/workflows/ci.yml",
				)
			},
		},
		{
			name: "the three baselines with other files",
			change: func(t *testing.T, dir string) {
				write(t, dir,
					"internal/scenario/testdata/baseline.json",
					"internal/scenario/testdata/counts.json",
					"internal/scenario/testdata/traffic.json",
					"internal/delivery/worker.go",
					"spec/delivery.md",
				)
			},
		},
		{
			name: "an end-to-end test with other files",
			change: func(t *testing.T, dir string) {
				write(t, dir, "internal/e2e/e2e_test.go", "internal/e2e/stack.go", "internal/delivery/worker.go")
			},
		},
		{
			name: "a baseline with a check file and another file",
			change: func(t *testing.T, dir string) {
				write(t, dir,
					"internal/scenario/testdata/baseline.json",
					"docs/testing.md",
					"Makefile",
				)
			},
			fails:  true,
			output: []string{"check files:\n  docs/testing.md\n", "other files:\n  Makefile\n"},
		},
		{
			// A check file moved away is a check file changed: the old
			// path counts as well as the new.
			name:     "a check file renamed out of the check files",
			existing: []string{"internal/scenario/limits.go"},
			change: func(t *testing.T, dir string) {
				if err := os.Mkdir(filepath.Join(dir, "internal/limits"), 0o755); err != nil {
					t.Fatal(err)
				}
				git(t, dir, "mv", "internal/scenario/limits.go", "internal/limits/limits.go")
			},
			fails:  true,
			output: []string{"check files:\n  internal/scenario/limits.go\n", "other files:\n  internal/limits/limits.go\n"},
		},
		{
			name:     "a check file deleted beside another change",
			existing: []string{"internal/scenario/margins.go"},
			change: func(t *testing.T, dir string) {
				git(t, dir, "rm", "-q", "internal/scenario/margins.go")
				write(t, dir, "README.md")
			},
			fails:  true,
			output: []string{"check files:\n  internal/scenario/margins.go\n", "other files:\n  README.md\n"},
		},
		{
			// git quotes such a path unless told not to, and a quoted
			// path matches no pattern.
			name: "a check file whose name git would quote",
			change: func(t *testing.T, dir string) {
				write(t, dir, "internal/scenario/é \"q\".go", "README.md")
			},
			fails:  true,
			output: []string{"check files:\n  internal/scenario/é \"q\".go\n", "other files:\n  README.md\n"},
		},
		{
			name: "no check files",
			change: func(t *testing.T, dir string) {
				write(t, dir, "internal/delivery/worker.go", "README.md")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			git(t, dir, "init", "-q", "-b", "main")
			write(t, dir, append([]string{"go.mod"}, tt.existing...)...)
			commit(t, dir, "base")
			git(t, dir, "checkout", "-q", "-b", "change")
			tt.change(t, dir)
			commit(t, dir, "change")
			// A commit on main after the branch started isn't the
			// branch's change.
			git(t, dir, "checkout", "-q", "main")
			write(t, dir, "internal/scenario/late.go")
			commit(t, dir, "later on main")
			git(t, dir, "checkout", "-q", "change")

			out, err := runChecksApart(t, dir, "main")
			var exit *exec.ExitError
			switch {
			case tt.fails && !errors.As(err, &exit):
				t.Fatalf("checks-apart.sh: err = %v, want exit status 1\n%s", err, out)
			case tt.fails && exit.ExitCode() != 1:
				t.Fatalf("checks-apart.sh: exit status %d, want 1\n%s", exit.ExitCode(), out)
			case !tt.fails && err != nil:
				t.Fatalf("checks-apart.sh: %v\n%s", err, out)
			}
			rest := out
			for _, want := range tt.output {
				i := strings.Index(rest, want)
				if i < 0 {
					t.Fatalf("output doesn't hold %q after what came before it:\n%s", want, out)
				}
				rest = rest[i+len(want):]
			}
		})
	}
}

func TestChecksApartFailsWithoutBase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "go.mod")
	commit(t, dir, "base")
	out, err := runChecksApart(t, dir, "no-such-ref")
	if err == nil {
		t.Fatalf("checks-apart.sh passed with a base that doesn't exist:\n%s", out)
	}
	if !strings.Contains(out, "no-such-ref") {
		t.Errorf("output doesn't name the base:\n%s", out)
	}
}

func runChecksApart(t *testing.T, dir, base string) (string, error) {
	t.Helper()
	script, err := filepath.Abs("checks-apart.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), script, base)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// write writes each file under dir, with content of its own, and stages it.
func write(t *testing.T, dir string, files ...string) {
	t.Helper()
	for _, f := range files {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(t.Name()+" "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", f)
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", msg)
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gitEnv is the environment without the caller's git settings, so that
// git works on the test's repository alone.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
}
