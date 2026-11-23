package scenario

import (
	"errors"
	"fmt"
	"strings"
)

// The harness's own Event type and filter matching, built from the spec
// rather than imported from Herald, so that a bug in Herald's matching
// can't become what the fan-out check expects (ADR 0007, "The harness").
// The grammar is spec/delivery.md's; the caps are R-31's and R-35's.

// validToken reports whether s is a `token`: one or more of a-z, 0-9
// and _.
func validToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// validEventType says why s isn't an Event type the Tenant API accepts
// (R-35), or returns nil if it is one.
func validEventType(s string) error {
	if len(s) > eventTypeBytes {
		return fmt.Errorf("longer than %d bytes", eventTypeBytes)
	}
	for _, tok := range strings.Split(s, ".") {
		if !validToken(tok) {
			return fmt.Errorf("%q doesn't match event-type", s)
		}
	}
	return nil
}

// validPattern says why s isn't a `pattern`, or returns nil if it is one.
func validPattern(s string) error {
	if len(s) > filterPatternBytes {
		return fmt.Errorf("pattern longer than %d bytes", filterPatternBytes)
	}
	toks := strings.Split(s, ".")
	for i, tok := range toks {
		if tok == "*" || validToken(tok) || (tok == ">" && i == len(toks)-1) {
			continue
		}
		return fmt.Errorf("%q doesn't match pattern", s)
	}
	return nil
}

// validFilter says why f isn't a filter the Tenant API accepts (R-31), or
// returns nil if it is one.
func validFilter(f []string) error {
	if len(f) > maxFilterPatterns {
		return fmt.Errorf("more than %d patterns", maxFilterPatterns)
	}
	var errs []error
	for _, p := range f {
		errs = append(errs, validPattern(p))
	}
	return errors.Join(errs...)
}

// matches reports whether filter matches eventType (R-2 to R-4). Both must
// be valid.
func matches(filter []string, eventType string) bool {
	if len(filter) == 0 {
		return true
	}
	toks := strings.Split(eventType, ".")
	for _, p := range filter {
		if patternMatches(strings.Split(p, "."), toks) {
			return true
		}
	}
	return false
}

// patternMatches matches a pattern's tokens with an Event type's, token by
// token: a final > matches one or more tokens, * any one, and any other
// token itself.
func patternMatches(pattern, toks []string) bool {
	for i, p := range pattern {
		if p == ">" {
			return len(toks) > i
		}
		if i >= len(toks) || (p != "*" && p != toks[i]) {
			return false
		}
	}
	return len(pattern) == len(toks)
}
