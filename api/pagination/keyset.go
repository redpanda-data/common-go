// Package pagination provides functions for handling paginated Redpanda API
// responses on the client and server-side.
package pagination

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"

	commonv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1alpha1"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrInvalidTokenFormat is returned when the given token cannot be base64-decoded.
	ErrInvalidTokenFormat = errors.New("token format is malformed")
	// ErrInvalidTokenKey is returned when the token key is invalid.
	ErrInvalidTokenKey = errors.New("invalid pagination token key")
	// ErrNoToken is returned when the given token is empty.
	ErrNoToken = errors.New("token is empty")
)

// DecodeToken decodes a pagination token.
func DecodeToken(tokenStr string, validKeys []string) (*commonv1.KeySetPageToken, error) {
	if tokenStr == "" {
		return nil, ErrNoToken
	}

	decoded, err := base64.StdEncoding.DecodeString(tokenStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTokenFormat, err)
	}
	var token commonv1.KeySetPageToken
	if err := proto.Unmarshal(decoded, &token); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTokenFormat, err)
	}

	if !slices.Contains(validKeys, token.Key) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTokenKey, token.Key)
	}

	return &token, nil
}

// SliceToPaginated converts a slice of objects into pages based on the
// pageSize and generates a pagination token. This function can be used by
// handlers to chop a slice into multiple pages for serving paginated responses.
// Make sure to provide the collection in the same, consistent order.
func SliceToPaginated[T any](arr []T, pageSize int, keyName string, keyGetter func(x T) string) (page []T, nextToken string, err error) {
	needsPagination := len(arr) > pageSize
	if !needsPagination {
		return arr, "", nil
	}

	token := commonv1.KeySetPageToken{
		Key:               keyName,
		ValueGreaterEqual: keyGetter(arr[pageSize]),
	}
	encoded, err := proto.Marshal(&token)
	if err != nil {
		return nil, "", err
	}
	tokenStr := base64.StdEncoding.EncodeToString(encoded)

	return arr[:pageSize], tokenStr, err
}

// SliceToPaginatedWithToken is the same as SliceToPaginated, but it accepts a pageToken in addition.
// This function is helpful in cases where you always want to pass the entire slice (including the items
// from previous pages), but want to retrieve the next page plus the right token for iterating further.
func SliceToPaginatedWithToken[T any](arr []T, pageSize int, pageToken, keyName string, keyGetter func(x T) string) (page []T, nextToken string, err error) {
	if pageToken == "" {
		return SliceToPaginated(arr, pageSize, keyName, keyGetter)
	}

	keysetPage, err := DecodeToken(pageToken, []string{keyName})
	if err != nil {
		return nil, "", err
	}

	// Find the index of the first page item inside arr so that we can start a new page
	firstPageItemIdx := 0
	for i, item := range arr {
		if keyGetter(item) == keysetPage.ValueGreaterEqual {
			firstPageItemIdx = i
			break
		}
	}

	return SliceToPaginated(arr[firstPageItemIdx:], pageSize, keyName, keyGetter)
}
