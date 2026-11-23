// Package spanschema reads the span schema (spec/span-schema.md) and checks
// spans against it: their names, their attributes and the attributes'
// types, the values of herald.outcome, and the trace shape. It imports none
// of the code that emits the spans, so the checks that use it grade that
// code by the spec, not by the code's own list.
package spanschema

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"uuid"

	"github.com/madskrogh/herald/spec"
)

// file is the span schema's file in the spec.
const file = "span-schema.md"

// Type is an attribute's type, as the schema names it.
type Type string

// The attribute types. Every value is text in the store; Int is a base-10
// integer and UUID a UUID in lowercase canonical text form.
const (
	String Type = "string"
	Int    Type = "int"
	UUID   Type = "uuid"
)

// Attribute is what the schema says of an attribute key.
type Attribute struct {
	Type Type
	// MayBeEmpty says whether the value may be empty text.
	MayBeEmpty bool
}

// Root says whether a span may be the root of its trace.
type Root string

// The values of Root, as the schema's Root column gives them.
const (
	// RootAlways: the span never has a parent.
	RootAlways Root = "always"
	// RootMay: the span may have a parent or none.
	RootMay Root = "may"
	// RootNever: the span always has a parent.
	RootNever Root = "never"
)

// SpanSchema is what the schema says of one span.
type SpanSchema struct {
	Service string
	// Parents are the spans that may be its parent.
	Parents []string
	Root    Root
	// Links are the spans it may link to.
	Links []string
	// Attributes are its attribute keys, in the schema's order.
	Attributes []string
	// Outcomes are the values its herald.outcome may take.
	Outcomes []string
}

// Schema is the span schema.
type Schema struct {
	Spans      map[string]SpanSchema
	Attributes map[string]Attribute
}

// Span is a span to check, as much of it as the schema states.
type Span struct {
	Name string
	// Service is the service that emitted it, or empty if unknown, which
	// isn't checked.
	Service string
	// Attributes holds its attributes as text, as the store holds them.
	Attributes map[string]string
	// Parent is the name of its parent span, or empty if it has none or
	// the parent isn't known. Root says which.
	Parent string
	Root   bool
	// Links are the names of the spans it links to that are known.
	Links []string
}

// outcomeKey is the attribute whose values the schema lists for each span.
const outcomeKey = "herald.outcome"

// load parses the schema file once.
var load = sync.OnceValues(func() (Schema, error) {
	data, err := spec.Files.ReadFile(file)
	if err != nil {
		return Schema{}, fmt.Errorf("read the span schema: %w", err)
	}
	s, err := parse(data)
	if err != nil {
		return Schema{}, fmt.Errorf("span schema %s: %w", file, err)
	}
	return s, nil
})

// Load returns the span schema in the spec.
func Load() (Schema, error) {
	return load()
}

// Check returns every way span breaks the schema: CheckAttributes's
// problems and CheckTrace's. A span the schema doesn't name has that one
// problem.
func (s Schema) Check(span Span) []error {
	if _, ok := s.Spans[span.Name]; !ok {
		return []error{notInSchema(span.Name)}
	}
	return append(s.CheckAttributes(span), s.CheckTrace(span)...)
}

func notInSchema(name string) error {
	return fmt.Errorf("span %q is not in the schema", name)
}

// CheckAttributes returns the ways span's name and attributes break the
// schema: a span it doesn't name; an attribute missing, or not among the
// span's; a value of another type, or empty where it may not be; and a
// herald.outcome the span may not take. Check is what callers use; tests use
// CheckAttributes on its own, for the vectors that give only attributes.
func (s Schema) CheckAttributes(span Span) []error {
	want, ok := s.Spans[span.Name]
	if !ok {
		return []error{notInSchema(span.Name)}
	}
	var problems []error
	for _, k := range want.Attributes {
		v, ok := span.Attributes[k]
		if !ok {
			problems = append(problems, fmt.Errorf("span %q: attribute %q is missing", span.Name, k))
			continue
		}
		if err := s.Attributes[k].check(v); err != nil {
			problems = append(problems, fmt.Errorf("span %q: attribute %q: %w", span.Name, k, err))
			continue
		}
		if k == outcomeKey && !slices.Contains(want.Outcomes, v) {
			problems = append(problems, fmt.Errorf("span %q: attribute %q is %q, not one of %q", span.Name, k, v, want.Outcomes))
		}
	}
	for _, k := range sortedKeys(span.Attributes) {
		if !slices.Contains(want.Attributes, k) {
			problems = append(problems, fmt.Errorf("span %q: attribute %q is not in the schema", span.Name, k))
		}
	}
	return problems
}

// CheckTrace returns the ways span's place in its trace breaks the schema:
// a service other than the schema's, a parent it may not have, no parent
// where it must have one, a parent where it may have none, and a link to a
// span it may not link to. Check is what callers use; tests use CheckTrace
// on its own, for the vectors that give only the trace shape.
func (s Schema) CheckTrace(span Span) []error {
	want, ok := s.Spans[span.Name]
	if !ok {
		return []error{notInSchema(span.Name)}
	}
	var problems []error
	if span.Service != "" && span.Service != want.Service {
		problems = append(problems, fmt.Errorf("span %q: from %s, want %s", span.Name, span.Service, want.Service))
	}
	switch {
	case span.Root && want.Root == RootNever:
		problems = append(problems, fmt.Errorf("span %q: no parent, want one of %q", span.Name, want.Parents))
	case !span.Root && want.Root == RootAlways:
		parent := "a span not among those checked"
		if span.Parent != "" {
			parent = fmt.Sprintf("%q", span.Parent)
		}
		problems = append(problems, fmt.Errorf("span %q: parent %s, want none", span.Name, parent))
	case span.Parent != "" && !slices.Contains(want.Parents, span.Parent):
		problems = append(problems, fmt.Errorf("span %q: parent %q, want one of %q", span.Name, span.Parent, want.Parents))
	}
	for _, l := range span.Links {
		if !slices.Contains(want.Links, l) {
			problems = append(problems, fmt.Errorf("span %q: links to %q, want only %q", span.Name, l, want.Links))
		}
	}
	return problems
}

// check returns why v isn't a value of a, or nil.
func (a Attribute) check(v string) error {
	if v == "" {
		if a.MayBeEmpty {
			return nil
		}
		return fmt.Errorf("empty, but it may not be")
	}
	switch a.Type {
	case Int:
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return fmt.Errorf("%q is not an int", v)
		}
	case UUID:
		if u, err := uuid.Parse(v); err != nil || u.String() != v {
			return fmt.Errorf("%q is not a UUID in lowercase canonical form", v)
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// The headings of the schema file's tables.
const (
	sectionSpans      = "Spans"
	sectionAttributes = "Attributes"
	sectionSpanAttrs  = "Attributes of each span"
	sectionOutcomes   = "Outcomes"
)

// The values of the schema's Root and Empty columns.
var (
	roots   = []Root{RootAlways, RootMay, RootNever}
	empties = map[string]bool{"allowed": true, "never": false}
	types   = []Type{String, Int, UUID}
)

var (
	headingRE    = regexp.MustCompile(`^#+\s+(.*?)\s*$`)
	backtickRE   = regexp.MustCompile("`([^`]*)`")
	backtickedRE = regexp.MustCompile("^`([^`]+)`$")
	delimiterRE  = regexp.MustCompile(`^:?-+:?$`)
)

// parse reads a span schema in the form of spec/span-schema.md: a table of
// spans, of attributes, of each span's attributes and of outcomes, each
// under its own "##" heading.
func parse(data []byte) (Schema, error) {
	tables, err := readTables(data)
	if err != nil {
		return Schema{}, err
	}
	s := Schema{Spans: map[string]SpanSchema{}, Attributes: map[string]Attribute{}}
	for _, name := range []string{sectionSpans, sectionAttributes, sectionSpanAttrs, sectionOutcomes} {
		if len(tables[name]) == 0 {
			return Schema{}, fmt.Errorf("no table under %q", "## "+name)
		}
	}
	for _, r := range tables[sectionSpans] {
		name, err := r.backticked("Span")
		if err != nil {
			return Schema{}, err
		}
		if _, ok := s.Spans[name]; ok {
			return Schema{}, r.errorf("span %q listed twice", name)
		}
		root := Root(r.cells["Root"])
		if !slices.Contains(roots, root) {
			return Schema{}, r.errorf("Root %q is not one of %q", root, roots)
		}
		parents := backticks(r.cells["Parent"])
		switch {
		case root == RootNever && len(parents) == 0:
			return Schema{}, r.errorf("span %q has no parent and may not be a root", name)
		case root == RootAlways && len(parents) > 0:
			return Schema{}, r.errorf("span %q is always a root, but has parents", name)
		}
		s.Spans[name] = SpanSchema{
			Service: r.cells["Service"],
			Parents: parents,
			Root:    root,
			Links:   backticks(r.cells["Links to"]),
		}
	}
	for _, r := range tables[sectionAttributes] {
		key, err := r.backticked("Key")
		if err != nil {
			return Schema{}, err
		}
		if _, ok := s.Attributes[key]; ok {
			return Schema{}, r.errorf("attribute %q listed twice", key)
		}
		t := Type(r.cells["Type"])
		if !slices.Contains(types, t) {
			return Schema{}, r.errorf("attribute %q: type %q is not one of %q", key, t, types)
		}
		empty, ok := empties[r.cells["Empty"]]
		if !ok {
			return Schema{}, r.errorf("attribute %q: Empty %q is not allowed or never", key, r.cells["Empty"])
		}
		s.Attributes[key] = Attribute{Type: t, MayBeEmpty: empty}
	}
	for _, r := range tables[sectionSpanAttrs] {
		name, err := r.backticked("Span")
		if err != nil {
			return Schema{}, err
		}
		span, ok := s.Spans[name]
		if !ok {
			return Schema{}, r.errorf("span %q is not under %q", name, "## "+sectionSpans)
		}
		if len(span.Attributes) > 0 {
			return Schema{}, r.errorf("span %q's attributes listed twice", name)
		}
		for _, k := range backticks(r.cells["Attributes"]) {
			if _, ok := s.Attributes[k]; !ok {
				return Schema{}, r.errorf("span %q: attribute %q is not under %q", name, k, "## "+sectionAttributes)
			}
			if slices.Contains(span.Attributes, k) {
				return Schema{}, r.errorf("span %q: attribute %q listed twice", name, k)
			}
			span.Attributes = append(span.Attributes, k)
		}
		s.Spans[name] = span
	}
	for _, r := range tables[sectionOutcomes] {
		name, err := r.backticked("Span")
		if err != nil {
			return Schema{}, err
		}
		span, ok := s.Spans[name]
		if !ok {
			return Schema{}, r.errorf("span %q is not under %q", name, "## "+sectionSpans)
		}
		v, err := r.backticked("`" + outcomeKey + "`")
		if err != nil {
			return Schema{}, err
		}
		if slices.Contains(span.Outcomes, v) {
			return Schema{}, r.errorf("span %q: outcome %q listed twice", name, v)
		}
		span.Outcomes = append(span.Outcomes, v)
		s.Spans[name] = span
	}
	for name, span := range s.Spans {
		if len(span.Attributes) == 0 {
			return Schema{}, fmt.Errorf("span %q has no attributes under %q", name, "## "+sectionSpanAttrs)
		}
		if slices.Contains(span.Attributes, outcomeKey) && len(span.Outcomes) == 0 {
			return Schema{}, fmt.Errorf("span %q has no outcomes under %q", name, "## "+sectionOutcomes)
		}
		for _, p := range append(slices.Clone(span.Parents), span.Links...) {
			if _, ok := s.Spans[p]; !ok {
				return Schema{}, fmt.Errorf("span %q names span %q, which is not in the schema", name, p)
			}
		}
	}
	return s, nil
}

// row is a row of a table, its cells by column.
type row struct {
	line  int
	cells map[string]string
}

func (r row) errorf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", r.line, fmt.Sprintf(format, args...))
}

// backticked returns what the row's column holds in backticks, the whole
// cell: a span name, a key or a value.
func (r row) backticked(column string) (string, error) {
	m := backtickedRE.FindStringSubmatch(r.cells[column])
	if m == nil {
		return "", r.errorf("%s %q is not one name in backticks", column, r.cells[column])
	}
	return m[1], nil
}

// readTables returns the rows of the first table under each heading, by the
// heading's text.
func readTables(data []byte) (map[string][]row, error) {
	tables := map[string][]row{}
	var (
		section string
		header  []string
		done    bool // whether the section's table has ended
	)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if m := headingRE.FindStringSubmatch(text); m != nil {
			section, header, done = m[1], nil, false
			continue
		}
		if !strings.HasPrefix(text, "|") {
			if header != nil {
				done = true
			}
			continue
		}
		if section == "" || done {
			continue
		}
		cells := splitRow(text)
		switch {
		case header == nil:
			header = cells
			continue
		case isDelimiter(cells):
			continue
		case len(cells) != len(header):
			return nil, fmt.Errorf("line %d: %d cells, want %d", line, len(cells), len(header))
		}
		r := row{line: line, cells: map[string]string{}}
		for i, h := range header {
			r.cells[h] = cells[i]
		}
		tables[section] = append(tables[section], r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read tables: %w", err)
	}
	return tables, nil
}

// splitRow returns the cells of a table row. An escaped pipe, \|, is part of
// its cell, as a pipe.
func splitRow(text string) []string {
	text = strings.TrimSuffix(strings.TrimPrefix(text, "|"), "|")
	var (
		cells []string
		cell  strings.Builder
	)
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\\' && i+1 < len(text) && text[i+1] == '|':
			cell.WriteByte('|')
			i++
		case text[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(text[i])
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// isDelimiter reports whether cells are a table's delimiter row, such as
// |---|:--:|.
func isDelimiter(cells []string) bool {
	for _, c := range cells {
		if !delimiterRE.MatchString(c) {
			return false
		}
	}
	return true
}

func backticks(cell string) []string {
	var out []string
	for _, m := range backtickRE.FindAllStringSubmatch(cell, -1) {
		out = append(out, m[1])
	}
	return out
}
