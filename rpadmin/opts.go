// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package rpadmin

import (
	"crypto/tls"
	"time"
)

// Opt is an option to configure an Admin API client.
type Opt interface{ apply(*AdminAPI) }

type clientOpt struct{ fn func(*AdminAPI) }

func (opt clientOpt) apply(cl *AdminAPI) { opt.fn(cl) }

// ClientTimeout sets the client timeout of any http client used for the
// Admin client (retry or oneshot), overriding the default of 10s.
func ClientTimeout(t time.Duration) Opt {
	return clientOpt{func(cl *AdminAPI) {
		if cl.retryClient != nil {
			cl.retryClient.Timeout = t
		}
		if cl.oneshotClient != nil {
			cl.oneshotClient.Timeout = t
		}
	}}
}

// MaxRetries sets the client maxRetries, overriding the default of 3.
func MaxRetries(r int) Opt {
	return clientOpt{func(cl *AdminAPI) {
		if cl.retryClient != nil {
			cl.retryClient.MaxRetries = r
		}
	}}
}

// Dialer sets a custom dialer for the Admin API client.
func Dialer(d DialContextFunc) Opt {
	return clientOpt{func(cl *AdminAPI) {
		cl.dialer = d
	}}
}

// TLS sets a custom TLS configuration for the Admin API client.
func TLS(t *tls.Config) Opt {
	return clientOpt{func(cl *AdminAPI) {
		cl.tlsConfig = t
	}}
}

// Authorization sets a custom authorization mechanism for the Admin API client.
func Authorization(a Auth) Opt {
	return clientOpt{func(cl *AdminAPI) {
		cl.auth = a
	}}
}

// BearerTokenAuth sets bearer token authentication for the Admin API client.
func BearerTokenAuth(token string) Opt {
	return clientOpt{func(cl *AdminAPI) {
		cl.auth = &BearerToken{Token: token}
	}}
}

// BasicAccessAuth sets basic access authentication for the Admin API client.
func BasicAccessAuth(user, pass string) Opt {
	return clientOpt{func(cl *AdminAPI) {
		cl.auth = &BasicAuth{Username: user, Password: pass}
	}}
}

// ForRedpandaCloud sets whether the client is intended to be used for Redpanda
// Cloud. This effectively adds the /api prefix to each request.
func ForRedpandaCloud(cloud bool) Opt {
	return clientOpt{func(cl *AdminAPI) {
		cl.forCloud = cloud
	}}
}
