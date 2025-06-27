package useragent

import (
	"context"
	"net/http"

	"github.com/ua-parser/uap-go/uaparser"
)

// HTTPMiddleware is the middlewared definition for the user agent extractor implementation
type HTTPMiddleware struct {
	uaParser *uaparser.Parser
}

// NewHTTPMiddleware creates a new UserAgentHTTPMiddleware.
func NewHTTPMiddleware() (*HTTPMiddleware, error) {
	parser, err := uaparser.NewFromBytes(definitionYaml)
	if err != nil {
		return nil, err
	}
	return &HTTPMiddleware{
		uaParser: parser,
	}, nil
}

// Handler is an http middleware that extracts the user agent from the request and stores it in the context.
func (i *HTTPMiddleware) Handler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		if userAgent := req.Header.Get("user-agent"); userAgent != "" {
			uaClient := i.uaParser.Parse(userAgent)
			userAgent := Parsed{
				Original:  userAgent,
				UserAgent: uaClient.UserAgent,
				Os:        uaClient.Os,
				Device:    uaClient.Device,
			}
			// Set the user agent in the context
			ctx = context.WithValue(ctx, ContextKey{}, userAgent)
		}

		// set the context in the req
		req = req.WithContext(ctx)
		handler.ServeHTTP(res, req)
	})
}
