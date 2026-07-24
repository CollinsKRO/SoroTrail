package api

import (
	"encoding/base64"
	"fmt"
)

// Cursor pagination contract:
//
//	All list endpoints accept ?cursor=<opaque> and ?limit=<int> query
//	parameters and return an envelope with a "next_cursor" field that is
//	non-null (a string) when more results exist and null/omitted when the
//	result set is exhausted.
//
//	The cursor value is the standard-base64 encoding of the last row's
//	sort key — an opaque token clients should treat as an opaque blob,
//	never inspected or modified. Sending an invalid cursor that cannot
//	be decoded returns a 400 Bad Request via the standard error envelope.
//
//	Default page size is 50; the maximum is 200.

const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// EncodeCursor base64-encodes a raw sort key into an opaque cursor token.
// The raw value is the last row's primary-key / sort-column value.
func EncodeCursor(raw string) string {
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor base64-decodes an opaque cursor back to its raw sort key.
// Returns an error (suitable for a 400 response) when the value is not
// valid base64.
func DecodeCursor(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid cursor: %s", err)
	}
	return string(raw), nil
}

// ResolvePageLimit returns a clamped limit value: zero/negative becomes
// DefaultPageSize, values exceeding MaxPageSize are capped.
func ResolvePageLimit(n int) int {
	if n <= 0 {
		return DefaultPageSize
	}
	if n > MaxPageSize {
		return MaxPageSize
	}
	return n
}


