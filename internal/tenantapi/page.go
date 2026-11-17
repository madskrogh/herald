package tenantapi

import (
	"encoding/base64"
	"uuid"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Page sizes for list calls, as documented in the protos.
const (
	defaultPageSize = 50
	maxPageSize     = 100
)

// page is the part of a list that a list call asks for: up to size results
// whose IDs come after after. Results are listed in ID order, and IDs are
// UUIDv7, so that is roughly the order they were created in. Paging on the
// ID rather than an offset keeps every page one index range scan, and a
// result created while a client pages through can't shift later pages.
type page struct {
	after uuid.UUID
	size  int32
}

// parsePage reads a list call's page_size and page_token.
func parsePage(size int32, token string) (page, error) {
	switch {
	case size < 0:
		return page{}, status.Errorf(codes.InvalidArgument, "page_size %d is negative", size)
	case size == 0:
		size = defaultPageSize
	case size > maxPageSize:
		size = maxPageSize
	}
	if token == "" {
		return page{size: size}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != len(uuid.UUID{}) {
		return page{}, status.Errorf(codes.InvalidArgument, "invalid page_token %q", token)
	}
	return page{after: uuid.UUID(b), size: size}, nil
}

// fetch is how many results to read for the page: one more than its size, so
// that a next page is offered only when there is one.
func (p page) fetch() int32 {
	return p.size + 1
}

// cut trims results, read with p.fetch(), to the page, and returns the
// page_token of the next page, or "" if this is the last.
func cut[T any](p page, results []T, id func(T) uuid.UUID) ([]T, string) {
	if len(results) <= int(p.size) {
		return results, ""
	}
	results = results[:p.size]
	last := id(results[len(results)-1])
	// The token is opaque to clients, so the encoding can change.
	return results, base64.RawURLEncoding.EncodeToString(last[:])
}
