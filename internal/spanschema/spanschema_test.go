package spanschema

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/madskrogh/herald/spec"
)

// TestCheckFollowsTheVectors checks each vector of the requirements on spans
// (spec/observability.md): a span the vector says follows the schema has no
// problems, and one that doesn't has a problem naming what breaks it.
func TestCheckFollowsTheVectors(t *testing.T) {
	t.Parallel()
	schema, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data, err := spec.Files.ReadFile("observability.md")
	if err != nil {
		t.Fatalf("read observability.md: %v", err)
	}
	tables, err := readTables(data)
	if err != nil {
		t.Fatalf("read observability.md: %v", err)
	}
	for _, req := range []struct {
		heading string
		check   func(Span) []error
	}{
		{"R-53 · implemented", schema.CheckAttributes},
		{"R-79 · implemented", schema.CheckAttributes},
		{"R-80 · implemented", schema.CheckAttributes},
		// Its vectors give the trace shape, not the attributes.
		{"R-81 · implemented", schema.CheckTrace},
	} {
		rows := tables[req.heading]
		if len(rows) == 0 {
			t.Fatalf("no vector under %q", req.heading)
		}
		for _, r := range rows {
			v := vector(r)
			t.Run(req.heading+"/line "+strconv.Itoa(r.line), func(t *testing.T) {
				t.Parallel()
				problems := req.check(v.span)
				switch {
				case v.follows && len(problems) > 0:
					t.Errorf("Check = %v, want no problems", problems)
				case !v.follows && len(problems) == 0:
					t.Errorf("Check found no problems, want one naming %q", v.names)
				case !v.follows:
					for _, p := range problems {
						if !names(p, v.span.Name, v.names) {
							t.Errorf("Check = %v, want only problems naming %q", problems, v.names)
						}
					}
				}
			})
		}
	}
}

// names reports whether problem p, with the span named span, names what:
// in what it says of the span, or by saying the span isn't in the schema
// when what is the span's own name. Every problem starts with the span's
// name, so that alone names nothing.
func names(p error, span, what string) bool {
	msg := p.Error()
	if what == span && msg == fmt.Sprintf("span %q is not in the schema", span) {
		return true
	}
	return strings.Contains(strings.TrimPrefix(msg, fmt.Sprintf("span %q:", span)), what)
}

// spanVector is one row of a vector table of spans.
type spanVector struct {
	span    Span
	follows bool
	// names is what a problem with the span names: an attribute, a span or
	// a service.
	names string
}

// vector reads a row of a vector table of spans, whose columns are named
// Span, Service, Attributes, Parent, Links to, Follows the schema and Names.
// An attribute is written `key=value`, and a Parent of none makes the span
// a root.
func vector(r row) spanVector {
	v := spanVector{
		follows: r.cells["Follows the schema"] == "yes",
		names:   strings.Trim(r.cells["Names"], "`"),
		span: Span{
			Name:       strings.Trim(r.cells["Span"], "`"),
			Service:    r.cells["Service"],
			Attributes: map[string]string{},
			Links:      backticks(r.cells["Links to"]),
		},
	}
	for _, kv := range backticks(r.cells["Attributes"]) {
		k, val, _ := strings.Cut(kv, "=")
		v.span.Attributes[k] = val
	}
	if parent := r.cells["Parent"]; parent == "none" {
		v.span.Root = true
	} else {
		v.span.Parent = strings.Trim(parent, "`")
	}
	return v
}
