package spec_test

import (
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/spec"
)

// TestFileTables checks what a shape file's reader needs from its tables:
// the heading each sits under, and the line of each row for its errors.
func TestFileTables(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"shape.md": &fstest.MapFile{Data: []byte(`# Shape

| Before | heading |
|---|---|
| a | b |

## Spans

Text.

| Span | Why |
|---|---|
| ` + "`attempt`" + ` | One Attempt. |
| ` + "`a|b`" + ` | A pipe in code. |

| Second | table |
|---|---|
| c | d |

## Attributes

| Key | Type |
|:--|--:|
| ` + "`herald.attempt`" + ` | int |
`)}}
	tables, err := spec.FileTables(fsys, "shape.md")
	if err != nil {
		t.Fatalf("FileTables: %v", err)
	}

	type table struct {
		Heading  string
		Line     int
		RowLines []int
		Rows     [][]string
	}
	var got []table
	for _, tb := range tables {
		tt := table{Heading: tb.Heading, Line: tb.Line, Rows: tb.Rows}
		for i := range tb.Rows {
			tt.RowLines = append(tt.RowLines, tb.RowLine(i))
		}
		got = append(got, tt)
	}
	want := []table{
		{Heading: "Shape", Line: 3, RowLines: []int{5}, Rows: [][]string{{"a", "b"}}},
		{Heading: "Spans", Line: 11, RowLines: []int{13, 14}, Rows: [][]string{{"`attempt`", "One Attempt."}, {"`a|b`", "A pipe in code."}}},
		{Heading: "Spans", Line: 16, RowLines: []int{18}, Rows: [][]string{{"c", "d"}}},
		{Heading: "Attributes", Line: 22, RowLines: []int{24}, Rows: [][]string{{"`herald.attempt`", "int"}}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FileTables (-want +got):\n%s", diff)
	}
}
