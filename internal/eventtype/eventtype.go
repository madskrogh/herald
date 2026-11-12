// Package eventtype validates Event types and matches them against Endpoint
// filters.
package eventtype

import (
	"fmt"
	"regexp"
	"strings"
)

// maxEventTypeBytes is the longest Event type ParseEventType accepts. The cap
// keeps the Event types that Deliveries carry in a handoff batch from bringing
// it near the NATS maximum message size.
const maxEventTypeBytes = 128

// maxPatternBytes is the longest filter pattern ParseFilter accepts. It equals
// maxEventTypeBytes, so a filter can name every valid Event type.
const maxPatternBytes = maxEventTypeBytes

// maxFilterPatterns is the most patterns an Endpoint filter may hold. Fan-out
// matches every pattern of every Endpoint on the request path that accepts a
// Message.
const maxFilterPatterns = 32

// tokenRE matches one token of an Event type. Event types and filter patterns
// share it, so a filter can name every valid Event type.
var tokenRE = regexp.MustCompile(`^[a-z0-9_]+$`)

// EventType is an Event type that has been parsed and validated by
// ParseEventType. The zero EventType is not a valid Event type.
type EventType struct {
	// tokens holds the Event type split into its dot-separated tokens.
	tokens []string
}

// ParseEventType parses an Event type: dot-separated tokens of lowercase
// letters, digits and underscores, such as "invoice.paid"
// (`^[a-z0-9_]+(\.[a-z0-9_]+)*$`), at most 128 bytes long.
func ParseEventType(s string) (EventType, error) {
	if len(s) > maxEventTypeBytes {
		return EventType{}, fmt.Errorf("invalid event type: %d bytes, want at most %d", len(s), maxEventTypeBytes)
	}
	tokens := strings.Split(s, ".")
	for _, tok := range tokens {
		if !tokenRE.MatchString(tok) {
			return EventType{}, fmt.Errorf("invalid event type %q: want dot-separated tokens of [a-z0-9_]", s)
		}
	}
	return EventType{tokens: tokens}, nil
}

// String returns the Event type as the Tenant submitted it, such as
// "invoice.paid".
func (e EventType) String() string {
	return strings.Join(e.tokens, ".")
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
// or more tokens. A filter holds at most 32 patterns, each at most 128 bytes
// long. An empty list is a valid filter.
func ParseFilter(patterns []string) (Filter, error) {
	if len(patterns) > maxFilterPatterns {
		return Filter{}, fmt.Errorf("invalid filter: %d patterns, want at most %d", len(patterns), maxFilterPatterns)
	}
	f := Filter{patterns: make([][]string, 0, len(patterns))}
	for _, p := range patterns {
		if len(p) > maxPatternBytes {
			return Filter{}, fmt.Errorf("invalid filter pattern: %d bytes, want at most %d", len(p), maxPatternBytes)
		}
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
// Event type e. An empty filter, including the zero Filter, matches every
// Event type; otherwise a match on any one pattern is enough.
func (f Filter) Matches(e EventType) bool {
	if len(f.patterns) == 0 {
		return true
	}
	for _, p := range f.patterns {
		if matchPattern(p, e.tokens) {
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
