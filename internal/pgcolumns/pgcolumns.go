// Package pgcolumns reads the Postgres column list (spec/postgres-columns.md)
// and checks a database against it (R-84 and R-85 in spec/storage.md). It
// imports none of the code that writes those columns, so the check grades
// Herald's migrations by the spec, not by the code's own queries.
package pgcolumns

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/madskrogh/herald/internal/pgcolumns/pgcolumnsdb"
	internalspec "github.com/madskrogh/herald/internal/spec"
	"github.com/madskrogh/herald/spec"
)

// SetBy says who writes a column, as the list's Set by column gives it.
type SetBy string

// The values of SetBy.
const (
	// SetByScenario: the scenario writes it, to set up a run.
	SetByScenario SetBy = "scenario"
	// SetByDatabase: Postgres generates it when the scenario writes a row
	// without it.
	SetByDatabase SetBy = "database"
	// SetByHerald: Herald's services write it, and the scenario only reads
	// it.
	SetByHerald SetBy = "Herald"
)

// Column is what the list says of a column.
type Column struct {
	// Table is the table's name with its schema, such as ingest.tenants.
	Table string
	Name  string
	// Type is the column's type as Postgres names it, such as
	// "timestamp with time zone".
	Type string
	// Nullable says whether the column may be NULL.
	Nullable bool
	SetBy    SetBy
}

// Stored is a column as a database has it.
type Stored struct {
	// Table is the table's name with its schema, such as ingest.tenants.
	Table string
	Name  string
	// Type is the column's type as Postgres's format_type names it.
	Type       string
	Nullable   bool
	HasDefault bool
}

// file is the column list's file in the spec, and section the heading its
// table sits under.
const (
	file    = "postgres-columns.md"
	section = "Columns"
)

// The values of the list's Null column.
var nulls = map[string]bool{"allowed": true, "never": false}

var setBys = []SetBy{SetByScenario, SetByDatabase, SetByHerald}

var backtickedRE = regexp.MustCompile("^`([^`]+)`$")

// load parses the list once.
var load = sync.OnceValues(func() ([]Column, error) {
	tables, err := internalspec.FileTables(spec.Files, file)
	if err != nil {
		return nil, fmt.Errorf("read the column list: %w", err)
	}
	list, err := parse(tables)
	if err != nil {
		return nil, fmt.Errorf("column list %s: %w", file, err)
	}
	return list, nil
})

// Load returns the column list in the spec.
func Load() ([]Column, error) {
	return load()
}

// parse reads the column list from the tables of a file in the form of
// spec/postgres-columns.md: one table, under "## Columns".
func parse(tables []internalspec.Table) ([]Column, error) {
	i := slices.IndexFunc(tables, func(t internalspec.Table) bool { return t.Heading == section })
	if i < 0 {
		return nil, fmt.Errorf("no table under %q", "## "+section)
	}
	t := tables[i]
	columns := map[string]int{}
	for _, name := range []string{"Table", "Column", "Type", "Null", "Set by", "Why"} {
		if columns[name] = t.Column(name); columns[name] < 0 {
			return nil, fmt.Errorf("line %d: no %q column", t.Line, name)
		}
	}
	var list []Column
	seen := map[string]bool{}
	for r, cells := range t.Rows {
		errorf := func(format string, args ...any) error {
			return fmt.Errorf("line %d: %s", t.RowLine(r), fmt.Sprintf(format, args...))
		}
		if len(cells) != len(t.Header) {
			return nil, errorf("%d cells, want %d", len(cells), len(t.Header))
		}
		cell := func(name string) string { return cells[columns[name]] }
		var c Column
		for _, f := range []struct {
			column string
			to     *string
		}{{"Table", &c.Table}, {"Column", &c.Name}} {
			m := backtickedRE.FindStringSubmatch(cell(f.column))
			if m == nil {
				return nil, errorf("%s %q is not one name in backticks", f.column, cell(f.column))
			}
			*f.to = m[1]
		}
		name := c.Table + "." + c.Name
		if seen[name] {
			return nil, errorf("%s listed twice", name)
		}
		seen[name] = true
		if c.Type = cell("Type"); c.Type == "" {
			return nil, errorf("%s has no type", name)
		}
		nullable, ok := nulls[cell("Null")]
		if !ok {
			return nil, errorf("%s: Null %q is not allowed or never", name, cell("Null"))
		}
		c.Nullable = nullable
		if c.SetBy = SetBy(cell("Set by")); !slices.Contains(setBys, c.SetBy) {
			return nil, errorf("%s: Set by %q is not one of %q", name, c.SetBy, setBys)
		}
		if cell("Why") == "" {
			return nil, errorf("%s has no reason", name)
		}
		list = append(list, c)
	}
	return list, nil
}

// Schemas returns the Postgres schemas the list's tables are in, such as
// ingest, in order.
func Schemas(list []Column) []string {
	var schemas []string
	for _, c := range list {
		schema, _, _ := strings.Cut(c.Table, ".")
		if !slices.Contains(schemas, schema) {
			schemas = append(schemas, schema)
		}
	}
	slices.Sort(schemas)
	return schemas
}

// Read returns the columns of every table in the given schemas of the
// database db reaches.
func Read(ctx context.Context, db pgcolumnsdb.DBTX, schemas []string) ([]Stored, error) {
	rows, err := pgcolumnsdb.New(db).ListColumns(ctx, schemas)
	if err != nil {
		return nil, fmt.Errorf("read the columns of %q: %w", schemas, err)
	}
	stored := make([]Stored, len(rows))
	for i, r := range rows {
		stored[i] = Stored{
			Table:      r.TableName,
			Name:       r.ColumnName,
			Type:       r.Type,
			Nullable:   r.Nullable,
			HasDefault: r.HasDefault,
		}
	}
	return stored, nil
}

// Check returns every way the columns stored break the list: R-84's
// columns, types and NULLs, and R-85's rows the scenario writes.
func Check(list []Column, stored []Stored) []error {
	byName := map[string]Stored{}
	for _, s := range stored {
		byName[s.Table+"."+s.Name] = s
	}
	listed := map[string]bool{}
	// written holds the tables the scenario writes rows of.
	written := map[string]bool{}
	var problems []error
	for _, c := range list {
		name := c.Table + "." + c.Name
		listed[name] = true
		if c.SetBy == SetByScenario {
			written[c.Table] = true
		}
		s, ok := byName[name]
		if !ok {
			problems = append(problems, fmt.Errorf("%s is missing", name))
			continue
		}
		if s.Type != c.Type {
			problems = append(problems, fmt.Errorf("%s is %s, want %s", name, s.Type, c.Type))
		}
		if s.Nullable && !c.Nullable {
			problems = append(problems, fmt.Errorf("%s may be NULL, but the list says never", name))
		}
		if c.SetBy == SetByDatabase && !s.HasDefault {
			problems = append(problems, fmt.Errorf("%s has no default, but the list says the database sets it", name))
		}
	}
	for _, s := range stored {
		name := s.Table + "." + s.Name
		if written[s.Table] && !listed[name] && !s.Nullable && !s.HasDefault {
			problems = append(problems, fmt.Errorf("%s is NOT NULL with no default, so the scenario can't write a row of %s", name, s.Table))
		}
	}
	return problems
}
