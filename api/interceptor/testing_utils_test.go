// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package interceptor_test

import (
	"context"

	"buf.build/gen/go/connectrpc/eliza/connectrpc/go/connectrpc/eliza/v1/elizav1connect"
	elizav1 "buf.build/gen/go/connectrpc/eliza/protocolbuffers/go/connectrpc/eliza/v1"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
)

type elizaServerHandler struct {
	elizav1connect.UnimplementedElizaServiceHandler
}

func (elizaServerHandler) Say(_ context.Context, req *connect.Request[elizav1.SayRequest]) (*connect.Response[elizav1.SayResponse], error) {
	if req.Msg.Sentence == "error" {
		return nil, connect.NewError(connect.CodeInternal, assert.AnError)
	}
	return connect.NewResponse(&elizav1.SayResponse{
		Sentence: req.Msg.Sentence, // Just echo request string
	}), nil
}

func (elizaServerHandler) Introduce(_ context.Context, req *connect.Request[elizav1.IntroduceRequest], stream *connect.ServerStream[elizav1.IntroduceResponse]) error {
	name := req.Msg.Name
	if name == "" {
		name = "Anonymous User"
	}
	if name == "error" {
		return connect.NewError(connect.CodeInternal, assert.AnError)
	}

	// Repeat the name multiple times (exactly len(name) times)
	repetitions := len(name)
	for i := 0; i < repetitions; i++ {
		if err := stream.Send(&elizav1.IntroduceResponse{Sentence: name}); err != nil {
			return err
		}
	}
	return nil
}
