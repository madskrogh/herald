package spanschema_test

import (
	"bufio"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/madskrogh/herald/internal/spanschema"
	"github.com/madskrogh/herald/spec"
)

// TestCheckFollowsTheVectors checks each vector of the requirements on spans
// (spec/observability.md): a span the vector says follows the schema has no
// problems, and one that doesn't has a problem naming what breaks it.
func TestCheckFollowsTheVectors(t *testing.T) {
	t.Parallel()
	schema, err := spanschema.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vectors := readVectors(t, "observability.md", "R-53", "R-71", "R-72", "R-73")
	for _, v := range vectors {
		t.Run(v.id+"/"+v.row, func(t *testing.T) {
			t.Parallel()
			check := schema.CheckAttributes
			if v.id == "R-73" {
				// Its vectors give the trace shape, not the attributes.
				check = schema.CheckTrace
			}
			problems := check(v.span)
			switch {
			case v.follows && len(problems) > 0:
				t.Errorf("Check = %v, want no problems", problems)
			case !v.follows && len(problems) == 0:
				t.Errorf("Check found no problems, want one naming %q", v.names)
			case !v.follows:
				for _, p := range problems {
					if !strings.Contains(p.Error(), v.names) {
						t.Errorf("Check = %v, want only problems naming %q", problems, v.names)
					}
				}
			}
		})
	}
}

// vector is one row of a vector table of spans.
type vector struct {
	id, row string
	span    spanschema.Span
	follows bool
	// names is what a problem with the span names: an attribute, a span or
	// a service.
	names string
}

var (
	headingRE  = regexp.MustCompile(`^#+\s+(R-[0-9]+)\b`)
	backtickRE = regexp.MustCompile("`([^`]*)`")
)

// readVectors reads the vector tables under the given requirements in a
// spec file. A table's columns are named by its header: Span, Service,
// Attributes, Parent, Links to, Follows the schema and Names.
func readVectors(t *testing.T, file string, ids ...string) []vector {
	t.Helper()
	f, err := spec.Files.Open(file)
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	defer f.Close()
	var (
		vectors []vector
		id      string
		header  []string
	)
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if m := headingRE.FindStringSubmatch(text); m != nil {
			id, header = "", nil
			if slices.Contains(ids, m[1]) {
				id = m[1]
			}
			continue
		}
		if id == "" || !strings.HasPrefix(text, "|") {
			header = nil
			continue
		}
		cells := cells(text)
		switch {
		case header == nil:
			header = cells
			continue
		case strings.HasPrefix(strings.TrimSpace(cells[0]), "---"):
			continue
		}
		row := map[string]string{}
		for i, h := range header {
			row[h] = cells[i]
		}
		v := vector{
			id:      id,
			row:     file + ":" + strconv.Itoa(line),
			follows: row["Follows the schema"] == "yes",
			names:   strings.Trim(row["Names"], "`"),
			span: spanschema.Span{
				Name:       strings.Trim(row["Span"], "`"),
				Service:    row["Service"],
				Attributes: map[string]string{},
			},
		}
		for _, kv := range backticks(row["Attributes"]) {
			k, val, _ := strings.Cut(kv, "=")
			v.span.Attributes[k] = val
		}
		if _, ok := row["Attributes"]; !ok {
			v.span.Attributes = nil
		}
		switch parent := row["Parent"]; parent {
		case "none":
			v.span.Root = true
		default:
			v.span.Parent = strings.Trim(parent, "`")
		}
		v.span.Links = backticks(row["Links to"])
		vectors = append(vectors, v)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	if len(vectors) == 0 {
		t.Fatalf("no vectors under %v in %s", ids, file)
	}
	return vectors
}

func cells(row string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

func backticks(cell string) []string {
	var out []string
	for _, m := range backtickRE.FindAllStringSubmatch(cell, -1) {
		out = append(out, m[1])
	}
	return out
}
