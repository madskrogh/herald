package main

import (
	"bytes"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const wantUsage = `usage: herald-admin <command> [arguments]

herald-admin is the operator CLI for Herald. It connects directly to the
ingest schema.

Commands:
  help    Show this help
`

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "no command",
			args:       nil,
			wantCode:   2,
			wantStderr: wantUsage,
		},
		{
			name:       "help",
			args:       []string{"help"},
			wantCode:   0,
			wantStdout: wantUsage,
		},
		{
			name:       "-h",
			args:       []string{"-h"},
			wantCode:   0,
			wantStdout: wantUsage,
		},
		{
			name:       "unknown command",
			args:       []string{"frobnicate"},
			wantCode:   2,
			wantStderr: "herald-admin: unknown command \"frobnicate\"\n\n" + wantUsage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d", tt.args, code, tt.wantCode)
			}
			if diff := cmp.Diff(tt.wantStdout, stdout.String()); diff != "" {
				t.Errorf("stdout (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantStderr, stderr.String()); diff != "" {
				t.Errorf("stderr (-want +got):\n%s", diff)
			}
		})
	}
}
