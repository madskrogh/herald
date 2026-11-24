package spec

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// Table is a Markdown table in the spec, such as a vector or the limits
// table, with each cell's text trimmed of spaces. Tests read it to build
// their cases from the spec (ADR 0013).
type Table struct {
	// Line is the line of the file its header is on.
	Line int
	// Heading is the text of the heading it sits under, or empty if it
	// comes before the first heading. A shape file's reader finds its
	// tables by heading.
	Heading string
	Header  []string
	Rows    [][]string
}

// RowLine returns the line of the file row i of Rows is on, for errors
// that point at it.
func (t Table) RowLine(i int) int {
	// A table's rows are consecutive lines, below its header and delimiter.
	return t.Line + 2 + i
}

// Record is a row of a table, its cells by the header's names, with the line
// it is on.
type Record struct {
	Line  int
	Cells map[string]string
}

// Records returns the rows of t as Records. A row with more or fewer cells
// than the header is an error, naming its line. A shape file's reader uses
// it to read each row by column.
func (t Table) Records() ([]Record, error) {
	records := make([]Record, 0, len(t.Rows))
	for i, cells := range t.Rows {
		r := Record{Line: t.RowLine(i), Cells: make(map[string]string, len(t.Header))}
		if len(cells) != len(t.Header) {
			return nil, fmt.Errorf("line %d: %d cells, want %d", r.Line, len(cells), len(t.Header))
		}
		for j, h := range t.Header {
			r.Cells[h] = cells[j]
		}
		records = append(records, r)
	}
	return records, nil
}

var backtickedRE = regexp.MustCompile("^`([^`]+)`$")

// Backticked returns what cell holds in backticks when that is the whole
// cell, a single name such as `ingest.tenants`, and whether it is.
func Backticked(cell string) (string, bool) {
	m := backtickedRE.FindStringSubmatch(cell)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Column returns the index of the header cell named name, or -1. Tests use
// it to find a column of a vector or the limits table.
func (t Table) Column(name string) int {
	for i, h := range t.Header {
		if h == name {
			return i
		}
	}
	return -1
}

// newTable reads the lines of a table, its header and delimiter rows
// included, that starts at line under heading.
func newTable(heading string, line int, lines []string) Table {
	t := Table{Line: line, Heading: heading, Header: cells(lines[0])}
	for _, l := range lines[2:] {
		t.Rows = append(t.Rows, cells(l))
	}
	return t
}

// cells splits a table row into its cells. A pipe inside a code span, or
// escaped as \|, doesn't split it.
func cells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	var (
		out     []string
		cell    strings.Builder
		code    bool
		escaped bool
	)
	for _, r := range row {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '`':
			code = !code
		case r == '|' && !code:
			out = append(out, strings.TrimSpace(cell.String()))
			cell.Reset()
			continue
		}
		cell.WriteRune(r)
	}
	return append(out, strings.TrimSpace(cell.String()))
}

// Vectors returns the vectors of requirement id: the tables under its
// heading, and under headings in vector files that name it. It fails if
// the spec has none. Tests use it to build their cases from a
// requirement's vectors.
func Vectors(fsys fs.FS, id string) ([]Table, error) {
	var tables []Table
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(name) != ".md" {
			return nil
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		for _, s := range parse(data) {
			if idLikeRE.FindString(s.heading) == id {
				tables = append(tables, s.tables...)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the vectors of %s: %w", id, err)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("the spec has no vector for %s", id)
	}
	return tables, nil
}

// FileTables returns every table in the spec's file name, such as
// "limits.md". It fails if the file has none. Tests use it to compare
// their limits with the spec's.
func FileTables(fsys fs.FS, name string) ([]Table, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("read the tables of %s: %w", name, err)
	}
	var tables []Table
	for _, s := range parse(data) {
		tables = append(tables, s.tables...)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("%s has no table", name)
	}
	return tables, nil
}
