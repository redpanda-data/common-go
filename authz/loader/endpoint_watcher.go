// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package loader

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"buf.build/gen/go/redpandadata/common/connectrpc/go/redpanda/policymaterializer/v1/policymaterializerv1connect"
	policymaterializerv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/policymaterializer/v1"
	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	"github.com/redpanda-data/common-go/authz"
)

var (
	// initTimeout is how long WatchPolicyFromEndpoint waits for the first
	// policy message before returning an InitializeWatchError.
	initTimeout = 30 * time.Second

	// reconnectBackoffInitial is the starting delay between reconnect attempts.
	reconnectBackoffInitial = time.Second

	// reconnectBackoffMax caps the exponential backoff.
	reconnectBackoffMax = 2 * time.Minute
)

// EndpointConfig configures the connection to a policy-materializer endpoint.
type EndpointConfig struct {
	// Address is the base URL of the policy-materializer Connect server,
	// e.g. "https://policy-materializer:9091" or "http://localhost:9091".
	Address string

	// TLS optionally configures TLS for the connection. When nil the client
	// uses a plain HTTP/2 (h2c) transport, suitable for in-cluster plaintext.
	TLS *tls.Config
}

// WatchPolicyFromEndpoint connects to a policy-materializer endpoint and
// streams policy updates. The first policy received from the stream is returned
// immediately, and the callback is invoked on every subsequent update.
//
// The connection is maintained automatically: if the stream is interrupted,
// WatchPolicyFromEndpoint reconnects with exponential backoff and calls the
// callback with the error before retrying so callers can log or act on it.
//
// Cancel ctx to stop watching and release resources.
func WatchPolicyFromEndpoint(ctx context.Context, cfg EndpointConfig, callback PolicyCallback) (authz.Policy, error) {
	httpClient := newHTTPClient(cfg.TLS)
	client := policymaterializerv1connect.NewPolicyMaterializerServiceClient(httpClient, cfg.Address)

	// innerCtx lets us cancel the background goroutine immediately if init
	// fails, without waiting for the caller to cancel their own context.
	innerCtx, cancel := context.WithCancel(ctx)

	initPolicy := make(chan authz.Policy, 1)
	initErr := make(chan error, 1)
	go maintainPolicyMaterializerStream(innerCtx, client, callback, initPolicy, initErr)

	t := time.NewTimer(initTimeout)
	defer t.Stop()

	select {
	case p := <-initPolicy:
		return p, nil
	case err := <-initErr:
		cancel()
		return authz.Policy{}, &InitializeWatchError{Err: err}
	case <-t.C:
		cancel()
		return authz.Policy{}, &InitializeWatchError{Err: errors.New("timed out waiting for initial policy from endpoint")}
	case <-ctx.Done():
		cancel()
		return authz.Policy{}, ctx.Err()
	}
}

// maintainPolicyMaterializerStream sends the first received policy on initPolicy (or a stream
// error on initErr), then calls callback on every subsequent update.
// It reconnects with jittered exponential backoff on disconnection, and runs
// until ctx is cancelled.
func maintainPolicyMaterializerStream(
	ctx context.Context,
	client policymaterializerv1connect.PolicyMaterializerServiceClient,
	callback PolicyCallback,
	initPolicy chan<- authz.Policy,
	initErr chan<- error,
) {
	backoff := reconnectBackoffInitial
	initialized := false

	for ctx.Err() == nil {
		err := openStream(ctx, client, func(p authz.Policy) {
			if !initialized {
				// First message: unblock WatchPolicyFromEndpoint.
				initialized = true
				select {
				case initPolicy <- p:
				case <-ctx.Done():
				}
				return
			}
			// Subsequent messages: forward to caller and reset backoff.
			callback(p, nil)
			backoff = reconnectBackoffInitial
		})

		if !initialized {
			// Stream ended before we got a single message — report the error
			// and stop; WatchPolicyFromEndpoint will surface it to the caller.
			select {
			case initErr <- err:
			case <-ctx.Done():
			}
			return
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(backoff)):
		}
		backoff = min(backoff*2, reconnectBackoffMax)
	}
}

// jitter returns d ±20% to spread reconnect attempts across multiple watchers.
func jitter(d time.Duration) time.Duration {
	// rand.Float64 returns [0, 1); scale to [-0.2, +0.2).
	factor := 1.0 + (rand.Float64()*0.4 - 0.2) //nolint:gosec // non-cryptographic jitter
	return time.Duration(float64(d) * factor)
}

// openStream opens a WatchPolicy stream and calls onPolicy for each
// message until the stream ends or ctx is cancelled. It returns the stream
// error (nil on clean cancellation).
func openStream(
	ctx context.Context,
	client policymaterializerv1connect.PolicyMaterializerServiceClient,
	onPolicy func(authz.Policy),
) error {
	stream, err := client.WatchPolicy(ctx, connect.NewRequest(&policymaterializerv1.WatchPolicyRequest{}))
	if err != nil {
		return fmt.Errorf("opening WatchPolicy stream: %w", err)
	}
	defer stream.Close()

	for stream.Receive() {
		msg := stream.Msg()
		if msg.GetPolicy() == nil {
			continue
		}
		onPolicy(toAuthzPolicy(msg.GetPolicy()))
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("WatchPolicy stream error: %w", err)
	}
	return nil
}

// toAuthzPolicy converts a policymaterializer DataplanePolicy to authz.Policy.
func toAuthzPolicy(p *policymaterializerv1.DataplanePolicy) authz.Policy {
	roles := make([]authz.Role, len(p.GetRoles()))
	for i, r := range p.GetRoles() {
		perms := make([]authz.PermissionName, len(r.GetPermissions()))
		for j, perm := range r.GetPermissions() {
			perms[j] = authz.PermissionName(perm)
		}
		roles[i] = authz.Role{
			ID:          authz.RoleID(r.GetId()),
			Permissions: perms,
		}
	}

	bindings := make([]authz.RoleBinding, len(p.GetBindings()))
	for i, b := range p.GetBindings() {
		bindings[i] = authz.RoleBinding{
			Role:      authz.RoleID(b.GetRoleId()),
			Principal: authz.PrincipalID(b.GetPrincipal()),
			Scope:     authz.ResourceName(b.GetScope()),
		}
	}

	return authz.Policy{Roles: roles, Bindings: bindings}
}

// newHTTPClient returns an HTTP client configured for Connect over HTTP/2.
// When tlsCfg is nil, an h2c (plaintext HTTP/2) transport is used. Go's
// default HTTP client uses HTTP/1.1 and cannot negotiate h2c, so we must
// configure an explicit http2.Transport with AllowHTTP: true. This is required
// for in-cluster plaintext gRPC/Connect traffic where TLS is not terminated at
// the pod.
func newHTTPClient(tlsCfg *tls.Config) *http.Client {
	if tlsCfg == nil {
		// AllowHTTP enables h2c (plaintext HTTP/2). DialTLSContext must also be
		// overridden to return a plain TCP connection — without it the transport
		// still attempts TLS negotiation even for http:// URLs, resulting in
		// "server gave HTTP response to HTTPS client".
		return &http.Client{
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			},
		}
	}
	return &http.Client{
		Transport: &http2.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
}
