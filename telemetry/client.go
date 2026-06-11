// Copyright 2026 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package telemetry provides a shared, anonymous telemetry client for Redpanda
// products. A Client signs a payload as an RS256 JWT and POSTs it; a Reporter
// runs a periodic collect→send loop with drop-on-error semantics.
package telemetry

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/go-resty/resty/v2"
)

const (
	// DefaultEndpoint is the telemetry ingestion host.
	DefaultEndpoint = "https://m.rp.vectorized.io"
	defaultTimeout  = 10 * time.Second
	defaultRetries  = 3
)

// Logger is the minimal logging surface the Reporter needs. Consumers adapt
// their own logger (slog, logr, benthos) with a tiny shim.
type Logger interface {
	Debug(msg string, keysAndValues ...any)
}

// Config configures the transport Client.
type Config struct {
	Endpoint      string
	Path          string
	UserAgent     string
	SigningKeyPEM []byte
	JWTHeaders    map[string]any
	Timeout       time.Duration
	// RetryCount is the number of transport-level retries (connection errors).
	// The zero value means "use the default" (3); a negative value disables
	// retries entirely. Note: resty only retries on transport errors, not on
	// HTTP error responses; the Reporter re-attempts the whole payload each
	// Period regardless.
	RetryCount int
}

// Client signs a payload as an RS256 JWT and POSTs it to Endpoint+Path.
type Client struct {
	resty    *resty.Client
	path     string
	signer   jose.Signer
	disabled bool
}

// New parses the signing key (if any) and builds the resty client + JOSE signer.
// An empty SigningKeyPEM yields a disabled Client whose Send is a no-op.
func New(cfg Config) (*Client, error) {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	// Zero means "use the default"; a negative value disables retries (resty's
	// SetRetryCount(0)). This keeps "no retries" expressible without colliding
	// with the unset zero value. A positive value is used as-is.
	retries := cfg.RetryCount
	if retries == 0 {
		retries = defaultRetries
	} else if retries < 0 {
		retries = 0
	}

	rc := resty.New().
		SetBaseURL(endpoint).
		SetTimeout(timeout).
		SetRetryCount(retries).
		SetHeader("User-Agent", cfg.UserAgent).
		SetHeader("Accept-Encoding", "gzip").
		SetHeader("Content-Type", "text/plain").
		SetHeader("Accept", "application/json")

	c := &Client{resty: rc, path: cfg.Path}

	if len(cfg.SigningKeyPEM) == 0 {
		c.disabled = true
		return c, nil
	}

	key, err := parseRSAPrivateKey(cfg.SigningKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parsing signing key: %w", err)
	}

	opts := (&jose.SignerOptions{}).WithType("JWT")
	for k, v := range cfg.JWTHeaders {
		opts.WithHeader(jose.HeaderKey(k), v)
	}

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, opts)
	if err != nil {
		return nil, fmt.Errorf("building signer: %w", err)
	}

	c.signer = signer
	return c, nil
}

// Disabled reports whether Send will no-op.
func (c *Client) Disabled() bool { return c.disabled }

// Send serializes payload as JWT claims, signs, and POSTs. No-op when disabled.
func (c *Client) Send(ctx context.Context, payload any) error {
	if c.disabled {
		return nil
	}

	tokenStr, err := josejwt.Signed(c.signer).Claims(payload).Serialize()
	if err != nil {
		return fmt.Errorf("serializing telemetry: %w", err)
	}

	resp, err := c.resty.R().SetContext(ctx).SetBody(tokenStr).Post(c.path)
	if err != nil {
		return fmt.Errorf("sending telemetry: %w", err)
	}

	if !resp.IsSuccess() {
		return fmt.Errorf("telemetry endpoint returned status %d", resp.StatusCode())
	}
	return nil
}

func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key (tried PKCS1 and PKCS8): %w", err)
	}

	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA private key")
	}

	return key, nil
}
