package tenantapi

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// maxApplicationNameBytes is the longest Application name, as documented in
// the protos and checked by the database.
const maxApplicationNameBytes = 256

// maxEndpointURLBytes is the longest Endpoint URL, as documented in the
// protos. It is the length most clients and servers accept.
const maxEndpointURLBytes = 2048

// checkApplicationName reports whether name can be an Application's name.
func checkApplicationName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("application name is blank")
	}
	if len(name) > maxApplicationNameBytes {
		return fmt.Errorf("application name is %d bytes, want at most %d", len(name), maxApplicationNameBytes)
	}
	return nil
}

// checkEndpointURL reports whether s can be an Endpoint's URL: an absolute
// http or https URL with a host, without user information, whose credentials
// would be stored in plain, or a fragment, which is never sent. Whitespace
// must be escaped, so that the URL registered is the URL requested.
func checkEndpointURL(s string) error {
	if len(s) > maxEndpointURLBytes {
		return fmt.Errorf("endpoint URL is %d bytes, want at most %d", len(s), maxEndpointURLBytes)
	}
	if strings.ContainsFunc(s, unicode.IsSpace) {
		return fmt.Errorf("endpoint URL %q contains whitespace", s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	// Parse lowercases the scheme.
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint URL %q: want an http or https URL", s)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("endpoint URL %q has no host", s)
	}
	if u.User != nil {
		return fmt.Errorf("endpoint URL %q has user information", s)
	}
	// Checked on the string: Parse drops an empty fragment.
	if strings.Contains(s, "#") {
		return fmt.Errorf("endpoint URL %q has a fragment", s)
	}
	return nil
}
