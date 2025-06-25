package useragent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPMiddleware(t *testing.T) {
	tests := []struct {
		name                string
		userAgent           string
		expUserAgentName    string
		expUserAgentVersion string
		expProviderVersion  string
	}{
		{
			name:                "Terraform user agent",
			userAgent:           "Terraform/1.0.1 (darwin_amd64) terraform-provider-aws/0.0.1",
			expUserAgentName:    "Terraform",
			expUserAgentVersion: "1.0.1",
			expProviderVersion:  "aws/0.0.1",
		},
		{
			name:                "rpk with full version",
			userAgent:           "rpk/v22.1.3 (darwin_arm64)",
			expUserAgentName:    "rpk",
			expUserAgentVersion: "22.1.3",
		},
		{
			name:                "rpk with suffix",
			userAgent:           "rpk/v22.1.3-rc1-commithash (darwin_arm64)",
			expUserAgentName:    "rpk",
			expUserAgentVersion: "22.1.3",
		},
		{
			name:                "rpk without version",
			userAgent:           "rpk/ (darwin_arm64)",
			expUserAgentName:    "rpk",
			expUserAgentVersion: "",
		},
		{
			name:                "rpk local dev build",
			userAgent:           "rpk/local-dev (darwin_arm64)",
			expUserAgentName:    "rpk",
			expUserAgentVersion: "", // We don't capture the version for local dev builds.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)
			middleware, err := NewHTTPMiddleware()
			require.NoError(t, err, "Expected no error while creating HTTPMiddleware")

			handler := middleware.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				ctx := r.Context()

				ua, ok := ExtractUserAgent(ctx)

				require.True(t, ok)

				assert.Equal(tt.expUserAgentName, ua.UserAgent.Family, "Expected user agent name to be: %v", tt.expUserAgentName)
				if tt.expUserAgentVersion != "" {
					assert.Equal(tt.expUserAgentVersion, ua.UserAgent.ToVersionString(), "Expected user agent version to be: %v", tt.expUserAgentVersion)
				}
				if tt.expProviderVersion != "" {
					// this is our own interpretation of terraform provider versioning
					assert.Equal(tt.expProviderVersion, ua.Device.Family, "Expected terraform provider version to be: %v", tt.expProviderVersion)
				}
			}))

			req := httptest.NewRequest(http.MethodGet, "http://example.com", http.NoBody)
			req.Header.Set("User-Agent", tt.userAgent)
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)
			assert.Equal(http.StatusOK, recorder.Code, "Expected status OK")
		})
	}
}
