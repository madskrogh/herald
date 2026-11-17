package eventtype_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/madskrogh/herald/internal/eventtype"
)

func TestParseEventType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		eventType string
		valid     bool
	}{
		{"one token", "invoice", true},
		{"two tokens", "invoice.paid", true},
		{"three tokens", "invoice.paid.late", true},
		{"digits and underscores", "user_2fa.enabled", true},
		{"underscore only", "_", true},
		{"128 bytes", strings.Repeat("a", 128), true},
		{"128 bytes, dotted", strings.Repeat("a.", 63) + "aa", true},
		{"empty", "", false},
		{"dot only", ".", false},
		{"leading dot", ".invoice", false},
		{"trailing dot", "invoice.", false},
		{"empty token", "invoice..paid", false},
		{"trailing newline", "invoice\n", false},
		{"uppercase", "Invoice.paid", false},
		{"hyphen", "invoice-paid", false},
		{"space", "invoice paid", false},
		{"single-token wildcard", "invoice.*", false},
		{"multi-token wildcard", "invoice.>", false},
		{"129 bytes", strings.Repeat("a", 129), false},
		{"129 bytes, dotted", strings.Repeat("a.", 64) + "a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, err := eventtype.ParseEventType(tt.eventType)
			if got := err == nil; got != tt.valid {
				t.Fatalf("ParseEventType(%q) error = %v, want valid %v", tt.eventType, err, tt.valid)
			}
			if tt.valid && e.String() != tt.eventType {
				t.Errorf("ParseEventType(%q).String() = %q, want the input", tt.eventType, e.String())
			}
		})
	}
}

func TestParseFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		valid    bool
	}{
		{"nil", nil, true},
		{"empty", []string{}, true},
		{"literal", []string{"invoice.paid"}, true},
		{"single-token wildcard", []string{"*"}, true},
		{"multi-token wildcard alone", []string{">"}, true},
		{"single-token wildcard last", []string{"invoice.*"}, true},
		{"single-token wildcard first", []string{"*.paid"}, true},
		{"single-token wildcards in a row", []string{"*.*.late"}, true},
		{"multi-token wildcard last", []string{"invoice.>"}, true},
		{"both wildcards", []string{"invoice.*.>"}, true},
		{"several patterns", []string{"invoice.*", "user.created"}, true},
		{"pattern of 128 bytes", []string{strings.Repeat("a", 128)}, true},
		{"pattern of 128 bytes, wildcards", []string{strings.Repeat("*.", 63) + "aa"}, true},
		{"32 patterns", slices.Repeat([]string{"invoice.*"}, 32), true},
		{"multi-token wildcard in the middle", []string{"invoice.>.late"}, false},
		{"multi-token wildcard first", []string{">.paid"}, false},
		{"partial single-token wildcard", []string{"inv*"}, false},
		{"partial multi-token wildcard", []string{"invoice.pa>"}, false},
		{"doubled wildcard", []string{"**"}, false},
		{"empty pattern", []string{""}, false},
		{"empty token", []string{"invoice..paid"}, false},
		{"trailing dot", []string{"invoice."}, false},
		{"uppercase", []string{"Invoice.paid"}, false},
		{"one invalid among valid", []string{"invoice.*", "user.>.x"}, false},
		{"pattern of 129 bytes", []string{strings.Repeat("a", 129)}, false},
		{"pattern of 129 bytes, wildcards", []string{strings.Repeat("*.", 64) + ">"}, false},
		{"33 patterns", slices.Repeat([]string{"invoice.*"}, 33), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := eventtype.ParseFilter(tt.patterns)
			if got := err == nil; got != tt.valid {
				t.Errorf("ParseFilter(%q) error = %v, want valid %v", tt.patterns, err, tt.valid)
			}
		})
	}
}

func TestFilterMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		patterns  []string
		eventType string
		want      bool
	}{
		{"empty filter", []string{}, "invoice.paid", true},
		{"nil filter", nil, "invoice", true},
		{"literal match", []string{"invoice.paid"}, "invoice.paid", true},
		{"literal mismatch", []string{"invoice.paid"}, "invoice.created", false},
		{"literal is not a prefix", []string{"invoice"}, "invoice.paid", false},
		{"literal longer than event type", []string{"invoice.paid"}, "invoice", false},
		{"* matches one token", []string{"invoice.*"}, "invoice.paid", true},
		{"* needs a token", []string{"invoice.*"}, "invoice", false},
		{"* matches only one token", []string{"invoice.*"}, "invoice.paid.late", false},
		{"* first", []string{"*.paid"}, "invoice.paid", true},
		{"* first, mismatch after", []string{"*.paid"}, "invoice.created", false},
		{"* alone", []string{"*"}, "invoice", true},
		{"* alone, two tokens", []string{"*"}, "invoice.paid", false},
		{"> matches one token", []string{"invoice.>"}, "invoice.paid", true},
		{"> matches several tokens", []string{"invoice.>"}, "invoice.paid.late", true},
		{"> needs a token", []string{"invoice.>"}, "invoice", false},
		{"> after a mismatch", []string{"invoice.>"}, "user.created", false},
		{"> alone", []string{">"}, "invoice.paid.late", true},
		{"* then >", []string{"*.paid.>"}, "invoice.paid.late", true},
		{"* then >, nothing for >", []string{"*.paid.>"}, "invoice.paid", false},
		{"any pattern is enough", []string{"user.*", "invoice.paid"}, "invoice.paid", true},
		{"no pattern matches", []string{"user.*", "invoice.paid"}, "invoice.created", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := eventtype.ParseFilter(tt.patterns)
			if err != nil {
				t.Fatalf("ParseFilter(%q): %v", tt.patterns, err)
			}
			if got := f.Matches(parseEventType(t, tt.eventType)); got != tt.want {
				t.Errorf("ParseFilter(%q).Matches(%q) = %v, want %v", tt.patterns, tt.eventType, got, tt.want)
			}
		})
	}
}

// FuzzFilter checks invariants that hold for any Tenant-supplied pattern and
// Event type: nothing panics, a valid Event type keeps its spelling, every
// valid Event type is a pattern that matches itself, and ">" matches every
// valid Event type. Seed inputs are in testdata/fuzz/FuzzFilter.
func FuzzFilter(f *testing.F) {
	f.Fuzz(func(t *testing.T, pattern, eventType string) {
		e, err := eventtype.ParseEventType(eventType)
		if err != nil {
			return
		}
		if filter, err := eventtype.ParseFilter([]string{pattern}); err == nil {
			filter.Matches(e)
		}
		if e.String() != eventType {
			t.Errorf("ParseEventType(%q).String() = %q, want the input", eventType, e.String())
		}

		self, err := eventtype.ParseFilter([]string{eventType})
		if err != nil {
			t.Fatalf("ParseFilter(%q) of a valid event type: %v", eventType, err)
		}
		if !self.Matches(e) {
			t.Errorf("ParseFilter(%q).Matches(%q) = false, want true", eventType, eventType)
		}
		all, err := eventtype.ParseFilter([]string{">"})
		if err != nil {
			t.Fatalf(`ParseFilter(">"): %v`, err)
		}
		if !all.Matches(e) {
			t.Errorf(`ParseFilter(">").Matches(%q) = false, want true`, eventType)
		}
	})
}

func TestZeroFilterMatchesEverything(t *testing.T) {
	t.Parallel()

	var f eventtype.Filter
	if !f.Matches(parseEventType(t, "invoice.paid")) {
		t.Error(`Filter{}.Matches("invoice.paid") = false, want true`)
	}
}

func parseEventType(t *testing.T, s string) eventtype.EventType {
	t.Helper()
	e, err := eventtype.ParseEventType(s)
	if err != nil {
		t.Fatalf("ParseEventType(%q): %v", s, err)
	}
	return e
}
