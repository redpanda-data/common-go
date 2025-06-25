package useragent

import (
	"context"

	"github.com/ua-parser/uap-go/uaparser"
)

// ContextKey is the key used to store the user agent in the context.
type ContextKey struct{}

// Parsed contains the information of the user agent extracted from the request header
type Parsed struct {
	Original  string
	UserAgent *uaparser.UserAgent
	Os        *uaparser.Os
	Device    *uaparser.Device
}

// ExtractUserAgent looks up the api caller's UserAgent via UserAgentContextKey
// in the context and returns the UserAgent, if it is present.
func ExtractUserAgent(ctx context.Context) (Parsed, bool) {
	val := ctx.Value(ContextKey{})
	if val != nil {
		if userAgent, ok := val.(Parsed); ok {
			return userAgent, true
		}
	}
	return Parsed{}, false
}
