package tenantapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	// Postgres text can't hold one.
	if strings.ContainsRune(name, 0) {
		return errors.New("application name contains a NUL byte")
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
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("endpoint URL %q: port %s is not 1 to 65535", s, port)
		}
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

// maxIdempotencyKeyBytes is the longest Idempotency-Key, as documented in the
// protos. Without a limit, a long key would fail to fit in the database's
// unique index, and the call with it.
const maxIdempotencyKeyBytes = 256

// checkIdempotencyKey reports whether key can be an Idempotency-Key: 1 to
// maxIdempotencyKeyBytes of printable ASCII, as HTTP defines the header (a
// structured-field string). Other bytes could also be invalid UTF-8, which
// Postgres text can't hold.
func checkIdempotencyKey(key string) error {
	if key == "" {
		return errors.New("Idempotency-Key is empty")
	}
	if len(key) > maxIdempotencyKeyBytes {
		return fmt.Errorf("Idempotency-Key is %d bytes, want at most %d", len(key), maxIdempotencyKeyBytes)
	}
	for i := range len(key) {
		if key[i] < ' ' || key[i] > '~' {
			return fmt.Errorf("Idempotency-Key %q: want printable ASCII", key)
		}
	}
	return nil
}

// maxPayloadBytes is the largest Message payload, as documented in the
// protos.
const maxPayloadBytes = 256 << 10

// checkPayload reports, as a status error, whether payload can be a Message's
// payload: a single JSON value of at most maxPayloadBytes. A payload that is
// too large is ResourceExhausted, as for a gRPC message over the server's
// limit, which the REST gateway answers with 413.
func checkPayload(payload string) error {
	if len(payload) > maxPayloadBytes {
		return status.Errorf(codes.ResourceExhausted, "message payload is %d bytes, want at most %d", len(payload), maxPayloadBytes)
	}
	if !json.Valid([]byte(payload)) {
		return status.Error(codes.InvalidArgument, "message payload is not a JSON value")
	}
	return nil
}

// invalidArgument returns err as the caller's error: an InvalidArgument status
// with err's message.
func invalidArgument(err error) error {
	return status.Error(codes.InvalidArgument, err.Error())
}

// parseID parses the ID of a resource of the given kind, such as
// "Application".
func parseID(kind, s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}, status.Errorf(codes.InvalidArgument, "invalid %s ID %q", kind, s)
	}
	return id, nil
}
