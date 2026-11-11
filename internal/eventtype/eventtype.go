// Package eventtype validates Event types and matches them against Endpoint
// filters.
package eventtype

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	eventTypeRE = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)
	tokenRE     = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// Validate returns an error if t is not a valid Event type: dot-separated
// tokens of lowercase letters, digits and underscores, such as "invoice.paid".
func Validate(t string) error {
	if !eventTypeRE.MatchString(t) {
		return fmt.Errorf("invalid event type %q: want dot-separated tokens of [a-z0-9_]", t)
	}
	return nil
}

// Filter is an Endpoint filter that has been parsed and validated by
// ParseFilter.
type Filter struct {
	// patterns holds each pattern split into its dot-separated tokens.
	patterns [][]string
}

// ParseFilter parses an Endpoint filter: a list of patterns with NATS-style
// wildcards. A pattern is an Event type in which any token may be "*", which
// matches exactly one token, and whose last token may be ">", which matches one
// or more tokens. An empty list is a valid filter.
func ParseFilter(patterns []string) (Filter, error) {
	f := Filter{patterns: make([][]string, 0, len(patterns))}
	for _, p := range patterns {
		tokens := strings.Split(p, ".")
		for i, tok := range tokens {
			if !validPatternToken(tok, i == len(tokens)-1) {
				return Filter{}, fmt.Errorf("invalid filter pattern %q: token %q", p, tok)
			}
		}
		f.patterns = append(f.patterns, tokens)
	}
	return f, nil
}

func validPatternToken(tok string, last bool) bool {
	switch tok {
	case "*":
		return true
	case ">":
		return last
	default:
		return tokenRE.MatchString(tok)
	}
}

// Matches reports whether an Endpoint with this filter accepts Messages of
// eventType. An invalid Event type matches no filter. An empty filter,
// including the zero Filter, matches every valid Event type; otherwise a match
// on any one pattern is enough.
func (f Filter) Matches(eventType string) bool {
	if Validate(eventType) != nil {
		return false
	}
	if len(f.patterns) == 0 {
		return true
	}
	tokens := strings.Split(eventType, ".")
	for _, p := range f.patterns {
		if matchPattern(p, tokens) {
			return true
		}
	}
	return false
}

func matchPattern(pattern, tokens []string) bool {
	for i, pt := range pattern {
		if pt == ">" {
			return len(tokens) > i
		}
		if i >= len(tokens) || (pt != "*" && pt != tokens[i]) {
			return false
		}
	}
	return len(pattern) == len(tokens)
}
