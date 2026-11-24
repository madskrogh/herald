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

	// TableUnder finds the first table under a heading, and none under a
	// heading with no table.
	for _, tt := range []struct {
		heading string
		line    int
		ok      bool
	}{{"Spans", 11, true}, {"Attributes", 22, true}, {"Outcomes", 0, false}} {
		got, ok := spec.TableUnder(tables, tt.heading)
		if got.Line != tt.line || ok != tt.ok {
			t.Errorf("TableUnder(%q) = table at line %d, %v, want line %d, %v", tt.heading, got.Line, ok, tt.line, tt.ok)
		}
	}
}

// TestFileTablesCells checks how a row splits into cells: a pipe in a code
// span or escaped as \| stays in its cell, and \| reads as |, as in
// GitHub's Markdown.
func TestFileTablesCells(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"shape.md": &fstest.MapFile{Data: []byte("| A | B |\n|---|---|\n" +
		"| `a|b` | c\\|d |\n" +
		"| `a\\|b` | c\\d |\n")}}
	tables, err := spec.FileTables(fsys, "shape.md")
	if err != nil {
		t.Fatalf("FileTables: %v", err)
	}
	want := [][]string{{"`a|b`", "c|d"}, {"`a|b`", `c\d`}}
	if diff := cmp.Diff(want, tables[0].Rows); diff != "" {
		t.Errorf("rows (-want +got):\n%s", diff)
	}
}

func TestTableRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		table   spec.Table
		want    []spec.Record
		wantErr string
	}{
		{
			name:  "cells by column, with their lines",
			table: spec.Table{Line: 10, Header: []string{"Span", "Why"}, Rows: [][]string{{"`attempt`", "One Attempt."}, {"`handoff`", ""}}},
			want: []spec.Record{
				{Line: 12, Cells: map[string]string{"Span": "`attempt`", "Why": "One Attempt."}},
				{Line: 13, Cells: map[string]string{"Span": "`handoff`", "Why": ""}},
			},
		},
		{
			name:    "a row with fewer cells than the header",
			table:   spec.Table{Line: 10, Header: []string{"Span", "Why"}, Rows: [][]string{{"`attempt`", "One Attempt."}, {"`handoff`"}}},
			wantErr: "line 13: 1 cells, want 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.table.Records()
			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != tt.wantErr {
				t.Fatalf("Records error = %q, want %q", gotErr, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Records (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBackticked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cell string
		want string
		ok   bool
	}{
		{cell: "`ingest.tenants`", want: "ingest.tenants", ok: true},
		{cell: "ingest.tenants"},
		{cell: "`a`, `b`"},
		{cell: "``"},
		{cell: "`a` and more"},
	}
	for _, tt := range tests {
		t.Run(tt.cell, func(t *testing.T) {
			t.Parallel()
			got, ok := spec.Backticked(tt.cell)
			if got != tt.want || ok != tt.ok {
				t.Errorf("Backticked(%q) = %q, %v, want %q, %v", tt.cell, got, ok, tt.want, tt.ok)
			}
		})
	}
}
