package eventtype_test

import (
	"testing"

	"github.com/madskrogh/herald/internal/eventtype"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		eventType string
		valid     bool
	}{
		{"invoice", true},
		{"invoice.paid", true},
		{"invoice.paid.late", true},
		{"user_2fa.enabled", true},
		{"_", true},
		{"", false},
		{".", false},
		{".invoice", false},
		{"invoice.", false},
		{"invoice..paid", false},
		{"Invoice.paid", false},
		{"invoice-paid", false},
		{"invoice paid", false},
		{"invoice.*", false},
		{"invoice.>", false},
	}
	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			t.Parallel()
			err := eventtype.Validate(tt.eventType)
			if got := err == nil; got != tt.valid {
				t.Errorf("Validate(%q) = %v, want valid %v", tt.eventType, err, tt.valid)
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
		{"invalid event type, empty filter", []string{}, "Invoice.paid", false},
		{"invalid event type, empty", []string{"*"}, "", false},
		{"invalid event type, trailing dot", []string{"invoice.*"}, "invoice.", false},
		{"invalid event type, uppercase", []string{">"}, "INVOICE", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := eventtype.ParseFilter(tt.patterns)
			if err != nil {
				t.Fatalf("ParseFilter(%q): %v", tt.patterns, err)
			}
			if got := f.Matches(tt.eventType); got != tt.want {
				t.Errorf("ParseFilter(%q).Matches(%q) = %v, want %v", tt.patterns, tt.eventType, got, tt.want)
			}
		})
	}
}

// FuzzFilter checks invariants that hold for any Tenant-supplied pattern and
// Event type: nothing panics, an invalid Event type matches no filter, every
// valid Event type is a pattern that matches itself, and ">" matches every
// valid Event type.
func FuzzFilter(f *testing.F) {
	f.Add("invoice.*", "invoice.paid")
	f.Add("invoice.>", "invoice")
	f.Add("*.paid.>", "invoice.paid.late")
	f.Add("invoice.>.late", "invoice.paid.late")
	f.Add("*", "")
	f.Fuzz(func(t *testing.T, pattern, eventType string) {
		valid := eventtype.Validate(eventType) == nil
		if filter, err := eventtype.ParseFilter([]string{pattern}); err == nil {
			if filter.Matches(eventType) && !valid {
				t.Errorf("ParseFilter(%q).Matches(%q) = true for an invalid event type", pattern, eventType)
			}
		}
		if !valid {
			return
		}

		self, err := eventtype.ParseFilter([]string{eventType})
		if err != nil {
			t.Fatalf("ParseFilter(%q) of a valid event type: %v", eventType, err)
		}
		if !self.Matches(eventType) {
			t.Errorf("ParseFilter(%q).Matches(%q) = false, want true", eventType, eventType)
		}
		all, err := eventtype.ParseFilter([]string{">"})
		if err != nil {
			t.Fatalf(`ParseFilter(">"): %v`, err)
		}
		if !all.Matches(eventType) {
			t.Errorf(`ParseFilter(">").Matches(%q) = false, want true`, eventType)
		}
	})
}

func TestZeroFilterMatchesEverything(t *testing.T) {
	t.Parallel()

	var f eventtype.Filter
	if !f.Matches("invoice.paid") {
		t.Error(`Filter{}.Matches("invoice.paid") = false, want true`)
	}
}
