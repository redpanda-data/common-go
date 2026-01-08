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
	"context"

	"buf.build/gen/go/redpandadata/core/connectrpc/go/redpanda/core/admin/v2/adminv2connect"
	"connectrpc.com/connect"
)

// BrokerService returns a client for the BrokerService of the Admin API V2.
func (a *AdminAPI) BrokerService(opts ...connect.ClientOption) adminv2connect.BrokerServiceClient {
	return adminv2connect.NewBrokerServiceClient(a, "/", withDefaultClientOpts(opts)...)
}

// ShadowLinkService returns a client for the ShadowLinkService of the Admin API V2.
func (a *AdminAPI) ShadowLinkService(opts ...connect.ClientOption) adminv2connect.ShadowLinkServiceClient {
	return adminv2connect.NewShadowLinkServiceClient(a, "/", withDefaultClientOpts(opts)...)
}

// ClusterService returns a client for the ClusterService of the Admin API V2.
func (a *AdminAPI) ClusterService(opts ...connect.ClientOption) adminv2connect.ClusterServiceClient {
	return adminv2connect.NewClusterServiceClient(a, "/", withDefaultClientOpts(opts)...)
}

// newContentTypeInterceptor is a Connect interceptor that sets the Content-Type
// and Accept headers to "application/proto" for all requests. This ensures that
// we override the default "application/json" used by some of our shared
// default http clients.
func newContentTypeInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			const applicationProto = "application/proto"
			req.Header().Set("Content-Type", applicationProto)
			req.Header().Set("Accept", applicationProto)

			return next(ctx, req)
		}
	}
}

func withDefaultClientOpts(opts []connect.ClientOption) []connect.ClientOption {
	return append([]connect.ClientOption{
		connect.WithInterceptors(newContentTypeInterceptor()),
	}, opts...)
}
