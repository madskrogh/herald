package scenario

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/madskrogh/herald/internal/spec"
)

// specFS is Herald's spec, which the harness's own matcher and schedule are
// built from (ADR 0007, "The harness").
var specFS = os.DirFS("../../spec")

// vectors returns the rows of requirement id's vectors, by column name.
func vectors(t *testing.T, id string) []map[string]string {
	t.Helper()
	tables, err := spec.Vectors(specFS, id)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	for _, tb := range tables {
		for _, r := range tb.Rows {
			row := map[string]string{}
			for i, h := range tb.Header {
				if i < len(r) {
					row[h] = r[i]
				}
			}
			rows = append(rows, row)
		}
	}
	return rows
}

var (
	codeRE   = regexp.MustCompile("`([^`]*)`")
	bytesRE  = regexp.MustCompile(`^(?:a )?([0-9]+)[ -]bytes?(?: pattern)?$`)
	countRE  = regexp.MustCompile(`^([0-9]+) patterns$`)
	filterRE = regexp.MustCompile(`^([A-Z]): (.*)$`)
)

// codeSpans returns the text of each code span in a cell, in order.
func codeSpans(cell string) []string {
	var out []string
	for _, m := range codeRE.FindAllStringSubmatch(cell, -1) {
		out = append(out, m[1])
	}
	return out
}

// vectorFilter reads a filter as a vector writes it: "(empty)" or "none",
// or its patterns as code spans.
func vectorFilter(t *testing.T, cell string) []string {
	t.Helper()
	switch cell {
	case "(empty)", "none":
		return nil
	case "(one empty pattern)":
		return []string{""}
	}
	if m := bytesRE.FindStringSubmatch(cell); m != nil {
		return []string{strings.Repeat("a", atoi(t, m[1]))}
	}
	if m := countRE.FindStringSubmatch(cell); m != nil {
		f := make([]string, atoi(t, m[1]))
		for i := range f {
			f[i] = "a"
		}
		return f
	}
	f := codeSpans(cell)
	if len(f) == 0 {
		t.Fatalf("can't read filter %q", cell)
	}
	return f
}

// vectorEventType reads an Event type as a vector writes it: "(empty)",
// "<n> bytes", or a code span.
func vectorEventType(t *testing.T, cell string) string {
	t.Helper()
	if cell == "(empty)" {
		return ""
	}
	if m := bytesRE.FindStringSubmatch(cell); m != nil {
		return strings.Repeat("a", atoi(t, m[1]))
	}
	if s := codeSpans(cell); len(s) == 1 {
		return s[0]
	}
	t.Fatalf("can't read Event type %q", cell)
	return ""
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func yes(t *testing.T, cell string) bool {
	t.Helper()
	switch cell {
	case "yes":
		return true
	case "no":
		return false
	}
	t.Fatalf("%q is neither yes nor no", cell)
	return false
}

func TestMatchesFollowsTheSpec(t *testing.T) {
	t.Parallel()

	type tc struct {
		filter    []string
		eventType string
		want      bool
	}
	var cases []tc
	for _, id := range []string{"R-2", "R-3"} {
		for _, row := range vectors(t, id) {
			cases = append(cases, tc{vectorFilter(t, row["Filter"]), vectorEventType(t, row["Event type"]), yes(t, row["Matches"])})
		}
	}
	for _, row := range vectors(t, "R-4") {
		cases = append(cases, tc{vectorFilter(t, row["Pattern"]), vectorEventType(t, row["Event type"]), yes(t, row["Matches"])})
	}
	for _, c := range cases {
		t.Run(strings.Join(c.filter, ",")+" "+c.eventType, func(t *testing.T) {
			t.Parallel()
			if got := matches(c.filter, c.eventType); got != c.want {
				t.Errorf("matches(%q, %q) = %v, want %v", c.filter, c.eventType, got, c.want)
			}
		})
	}
}

// TestFanOutFollowsTheSpec reads R-1's vector: each row names Endpoints and
// their filters, and the Endpoints a Message of the Event type reaches.
func TestFanOutFollowsTheSpec(t *testing.T) {
	t.Parallel()

	for _, row := range vectors(t, "R-1") {
		t.Run(row["Endpoints and their filters"]+" "+row["Event type"], func(t *testing.T) {
			t.Parallel()
			et := vectorEventType(t, row["Event type"])
			var got []string
			if eps := row["Endpoints and their filters"]; eps != "(no Endpoints)" {
				for _, ep := range strings.Split(eps, "; ") {
					m := filterRE.FindStringSubmatch(ep)
					if m == nil {
						t.Fatalf("can't read Endpoint %q", ep)
					}
					if matches(vectorFilter(t, m[2]), et) {
						got = append(got, m[1])
					}
				}
			}
			want := row["Deliveries to"]
			if g := strings.Join(got, ", "); g != want && (want != "none" || g != "") {
				t.Errorf("Deliveries to %q, want %q", g, want)
			}
		})
	}
}

func TestParseFollowsTheSpec(t *testing.T) {
	t.Parallel()

	created := func(t *testing.T, cell string) bool {
		t.Helper()
		if cell == "created" {
			return true
		}
		if strings.HasPrefix(cell, "`INVALID_ARGUMENT`") {
			return false
		}
		t.Fatalf("can't read result %q", cell)
		return false
	}
	for _, row := range vectors(t, "R-35") {
		t.Run("Event type "+row["Event type"], func(t *testing.T) {
			t.Parallel()
			et := vectorEventType(t, row["Event type"])
			if err, want := validEventType(et), created(t, row["Result"]); (err == nil) != want {
				t.Errorf("validEventType(%q) = %v, want valid: %v", et, err, want)
			}
		})
	}
	for _, row := range vectors(t, "R-31") {
		t.Run("filter "+row["Filter"], func(t *testing.T) {
			t.Parallel()
			f := vectorFilter(t, row["Filter"])
			if err, want := validFilter(f), created(t, row["Result"]); (err == nil) != want {
				t.Errorf("validFilter(%q) = %v, want valid: %v", f, err, want)
			}
		})
	}
}
