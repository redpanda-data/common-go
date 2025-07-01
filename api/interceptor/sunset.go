package interceptor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
)

const (
	// SunsetHeaderName is the HTTP header name as defined in RFC 8594
	// See: https://datatracker.ietf.org/doc/html/rfc8594
	SunsetHeaderName = "Sunset"

	// DeprecationHeaderName is an optional companion header as mentioned in RFC 8594
	DeprecationHeaderName = "Deprecation"

	// LinkHeaderName is an optional Link header that can be used in conjunction with Sunset
	LinkHeaderName = "Link"
)

var _ connect.Interceptor = &Sunset{}

// Sunset adds RFC 8594 compliant Sunset headers to API responses
// to signal API deprecation and inform clients about the planned removal date.
type Sunset struct {
	// sunsetDate is an IMF-fixdate formatted date (RFC 7231)
	sunsetDate string

	// deprecation is an optional value that can be:
	// - a date as @<unix-timestamp> as per RFC 9745
	// - Empty if not set
	deprecation string

	// linkValue is an optional Link header value with deprecation info
	// for example: '<https://example.com/deprecation>; rel="sunset"'
	linkValue string
}

// SunsetOption defines optional configurations for the SunsetInterceptor
type SunsetOption func(*Sunset)

// WithDeprecationDate sets the Deprecation header to a specific date
// when the API was deprecated
func WithDeprecationDate(date time.Time) SunsetOption {
	return func(s *Sunset) {
		// Format as @<unix-timestamp> as per RFC 9745
		s.deprecation = fmt.Sprintf("@%d", date.Unix())
	}
}

// WithLink adds a Link header with deprecation documentation
// as recommended by RFC 8594 Section 3
func WithLink(uri string) SunsetOption {
	return func(s *Sunset) {
		s.linkValue = "<" + uri + ">; rel=\"sunset\""
	}
}

// NewSunset creates a new interceptor that adds RFC 8594 compliant
// Sunset header to API responses.
func NewSunset(sunsetDate time.Time, opts ...SunsetOption) *Sunset {
	s := &Sunset{
		sunsetDate: formatRFC7231Date(sunsetDate),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// formatRFC7231Date formats a time according to RFC 7231 IMF-fixdate format
// which is required by RFC 8594
func formatRFC7231Date(t time.Time) string {
	return t.UTC().Format(time.RFC1123)
}

// WrapUnary adds Sunset headers to unary responses
func (s *Sunset) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// isClient checks if the request is a client-side request and should not have Sunset headers
		if req.Spec().IsClient {
			return next(ctx, req)
		}
		resp, err := next(ctx, req)
		if err != nil {
			if connectErr := new(connect.Error); errors.As(err, &connectErr) {
				// Modify the Connect error as needed
				// For example, add or modify metadata
				connectErr.Meta().Set(SunsetHeaderName, s.sunsetDate)
				if s.deprecation != "" {
					connectErr.Meta().Set(DeprecationHeaderName, s.deprecation)
				}
				if s.linkValue != "" {
					connectErr.Meta().Add(LinkHeaderName, s.linkValue)
				}
				return nil, connectErr
			}
			return nil, err
		}

		// this is an unlikely escape, but we handle it gracefully
		if resp != nil {
			// Add headers to indicate sunset
			resp.Header().Set(SunsetHeaderName, s.sunsetDate)

			if s.deprecation != "" {
				resp.Header().Set(DeprecationHeaderName, s.deprecation)
			}

			if s.linkValue != "" {
				resp.Header().Add(LinkHeaderName, s.linkValue)
			}
		}
		return resp, err
	}
}

// WrapStreamingClient is a no-op for client-side streams
func (*Sunset) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler adds Sunset headers to streaming responses
func (s *Sunset) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		// Always add headers to the streaming response, even if the handler later returns an error
		// Headers must be set before any data is sent
		conn.ResponseHeader().Set(SunsetHeaderName, s.sunsetDate)

		if s.deprecation != "" {
			conn.ResponseHeader().Set(DeprecationHeaderName, s.deprecation)
		}

		if s.linkValue != "" {
			conn.ResponseHeader().Add(LinkHeaderName, s.linkValue)
		}

		// Call the next handler
		err := next(ctx, conn)
		// We can't add headers after calling next, as the response may have already been sent

		return err
	}
}

// SunsetDate returns the IMF-fixdate formatted sunset date
func (s *Sunset) SunsetDate() string {
	return s.sunsetDate
}

// DeprecationDate returns the deprecation date in @<unix-timestamp> format
func (s *Sunset) DeprecationDate() string {
	return s.deprecation
}

// LinkValue returns the Link header value with deprecation info
func (s *Sunset) LinkValue() string {
	return s.linkValue
}
