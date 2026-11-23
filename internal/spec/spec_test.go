package spec_test

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/spec"
)

// TestSpecFollowsItsConventions checks the committed spec, so that make ci
// fails on its problems. TestCheck shows each problem is found.
func TestSpecFollowsItsConventions(t *testing.T) {
	t.Parallel()

	problems, err := spec.Check(os.DirFS("../../spec"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, p := range problems {
		t.Errorf("spec/%s", p)
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  []spec.Problem
	}{
		{
			name: "implemented MUST with no vector",
			files: map[string]string{
				"delivery.md": `# Delivery

### R-1 · implemented

When an Attempt gets a 2xx, Herald MUST NOT make another Attempt for that Delivery.

Why: ADR 0001.
`,
			},
			want: []spec.Problem{
				{File: "delivery.md", Line: 3, Message: "R-1 is an implemented MUST with no vector"},
			},
		},
		{
			name: "implemented MUST with an inline vector",
			files: map[string]string{
				"delivery.md": `### R-1 · implemented

When an Attempt gets a 2xx, Herald MUST NOT make another Attempt for that Delivery.

Why: ADR 0001.

| responses | attempts |
|-----------|----------|
| 500, 200  | 2        |
`,
			},
		},
		{
			name: "a table with no rows is no vector",
			files: map[string]string{
				"delivery.md": `### R-1 · implemented

Herald MUST NOT make another Attempt after a 2xx.

Why: ADR 0001.

| responses | attempts |
|-----------|----------|
`,
			},
			want: []spec.Problem{
				{File: "delivery.md", Line: 1, Message: "R-1 is an implemented MUST with no vector"},
			},
		},
		{
			name: "a table under the next heading is not the requirement's vector",
			files: map[string]string{
				"delivery.md": `### R-1 · implemented

Herald MUST NOT make another Attempt after a 2xx.

Why: ADR 0001.

## Retries

| responses | attempts |
|-----------|----------|
| 500, 200  | 2        |
`,
			},
			want: []spec.Problem{
				{File: "delivery.md", Line: 1, Message: "R-1 is an implemented MUST with no vector"},
			},
		},
		{
			name: "SHOULD, MAY, planned and withdrawn need no vector",
			files: map[string]string{
				"delivery.md": `### R-1 · implemented

Herald SHOULD make the first Attempt within a second.

Why: ADR 0001.

### R-2 · implemented

Herald MAY make an Attempt early.

Why: ADR 0001.

### R-3 · planned

Herald MUST keep Attempts for 30 days.

Why: ADR 0001.

### R-4 · withdrawn

Herald MUST retry 5 times.

Why: ADR 0001.

Withdrawn: replaced by R-5.
`,
			},
		},
		{
			name: "fenced code is not read",
			files: map[string]string{
				"README.md": "# Conventions\n\n```markdown\n### R-1 · implemented\n\nHerald MUST sign every Attempt.\n```\n",
			},
		},
		{
			name: "requirement heading without a status",
			files: map[string]string{
				"delivery.md": `### R-1 · implmented

Herald MUST NOT make another Attempt after a 2xx.

### R-2

Herald MUST sign every Attempt.
`,
			},
			want: []spec.Problem{
				{File: "delivery.md", Line: 1, Message: `R-1 has no status: write "R-1 · implemented", "R-1 · planned" or "R-1 · withdrawn"`},
				{File: "delivery.md", Line: 5, Message: `R-2 has no status: write "R-2 · implemented", "R-2 · planned" or "R-2 · withdrawn"`},
			},
		},
		{
			name: "an ID used twice",
			files: map[string]string{
				"api.md": `### R-1 · planned

Herald MAY return the Attempt history.

Why: ADR 0001.
`,
				"delivery.md": `### R-1 · planned

Herald SHOULD make the first Attempt within a second.

Why: ADR 0001.
`,
			},
			want: []spec.Problem{
				{File: "delivery.md", Line: 1, Message: "R-1 is already the ID of the requirement at api.md:1"},
			},
		},
		{
			name: "implemented MUST with a vector in a vector file",
			files: map[string]string{
				"delivery.md": `### R-1 · implemented

Herald MUST NOT make another Attempt after a 2xx.

Why: ADR 0001.
`,
				"vectors/no-attempt-after-2xx.md": `# No Attempt after a 2xx

## R-1

| responses | attempts |
|-----------|----------|
| 500, 200  | 2        |
`,
			},
		},
		{
			name: "a vector file names an unknown requirement",
			files: map[string]string{
				"delivery.md": `### R-1 · planned

Herald MUST NOT make another Attempt after a 2xx.

Why: ADR 0001.
`,
				"vectors/retry-schedule.md": `## R-9: random retry schedules

| failures | earliest Attempt |
|----------|------------------|
| 1        | 5s               |
`,
			},
			want: []spec.Problem{
				{File: "vectors/retry-schedule.md", Line: 1, Message: "R-9 names no requirement"},
			},
		},
		{
			name: "a vector file names a withdrawn requirement",
			files: map[string]string{
				"delivery.md": `### R-1 · withdrawn

Herald MUST retry 5 times.

Why: ADR 0001.

Withdrawn: replaced by R-2.
`,
				"vectors/retry-count.md": `## R-1

| failures | attempts |
|----------|----------|
| 5        | 6        |
`,
			},
		},
		{
			name: "a vector file's table under no requirement",
			files: map[string]string{
				"delivery.md": `### R-1 · implemented

Herald MUST NOT make another Attempt after a 2xx.

Why: ADR 0001.
`,
				"vectors/no-attempt-after-2xx.md": `## R-1

| responses | attempts |
|-----------|----------|
| 500, 200  | 2        |

## More cases

| responses | attempts |
|-----------|----------|
| 200       | 1        |
`,
			},
			want: []spec.Problem{
				{File: "vectors/no-attempt-after-2xx.md", Line: 9, Message: `vector is under no requirement: put it under a heading that starts with the requirement's ID, such as "## R-1"`},
			},
		},
		{
			name: "a long row doesn't end the file",
			files: map[string]string{
				"delivery.md": "### R-1 · implemented\n\nHerald MUST sign every Attempt.\n\nWhy: ADR 0003.\n\n" +
					"| payload |\n|---------|\n| " + strings.Repeat("a", 100_000) + " |\n\n" +
					"### R-2 · implemented\n\nHerald MUST NOT make another Attempt after a 2xx.\n\nWhy: ADR 0001.\n",
			},
			want: []spec.Problem{
				{File: "delivery.md", Line: 11, Message: "R-2 is an implemented MUST with no vector"},
			},
		},
		{
			name: "a vector file names an unknown requirement with no table under it",
			files: map[string]string{
				"vectors/retry-schedule.md": `## R-9

To come.
`,
			},
			want: []spec.Problem{
				{File: "vectors/retry-schedule.md", Line: 1, Message: "R-9 names no requirement"},
			},
		},
		{
			name: "a requirement with no reason",
			files: map[string]string{
				"delivery.md": `### R-1 · planned

Herald MAY make an Attempt early.

### R-2 · withdrawn

Herald MUST retry 5 times.

Why: ADR 0001.
`,
			},
			want: []spec.Problem{
				{File: "delivery.md", Line: 1, Message: "R-1 has no reason: add a \"Why:\" line"},
				{File: "delivery.md", Line: 5, Message: "R-2 is withdrawn with no reason: add a \"Withdrawn:\" line"},
			},
		},
		{
			name: "a MUST in the reason doesn't make the requirement a MUST",
			files: map[string]string{
				"delivery.md": `### R-1 · implemented

Herald SHOULD make the first Attempt within a second.

Why: Endpoints MUST see Messages quickly, or Tenants poll instead.

### R-2 · withdrawn

Herald SHOULD retry 5 times.

Why: ADR 0001.

Withdrawn: a MUST in R-3 replaces it.
`,
			},
		},
		{
			name: "a fence of the other kind doesn't close a code block",
			files: map[string]string{
				"README.md": "~~~markdown\n```abnf\n### R-1 · implemented\n\nHerald MUST sign every Attempt.\n~~~\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := fstest.MapFS{}
			for name, content := range tt.files {
				fsys[name] = &fstest.MapFile{Data: []byte(content)}
			}
			got, err := spec.Check(fsys)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Check problems (-want +got):\n%s", diff)
			}
		})
	}
}
