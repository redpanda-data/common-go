// Copyright 2025 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0
package mappers

import (
	"testing"
	"time"

	adminv2 "buf.build/gen/go/redpandadata/core/protocolbuffers/go/redpanda/core/admin/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestParseConnection(t *testing.T) {
	protoConn := &adminv2.KafkaConnection{
		NodeId:    2,
		ShardId:   4,
		Uid:       "36338ca5-86b7-4478-ad23-32d49cfaef61",
		State:     adminv2.KafkaConnectionState_KAFKA_CONNECTION_STATE_OPEN,
		OpenTime:  timestamppb.New(time.Date(2025, 10, 23, 1, 2, 3, 0, time.UTC)),
		CloseTime: nil,
		AuthenticationInfo: &adminv2.AuthenticationInfo{
			State:         adminv2.AuthenticationState_AUTHENTICATION_STATE_SUCCESS,
			Mechanism:     adminv2.AuthenticationMechanism_AUTHENTICATION_MECHANISM_MTLS,
			UserPrincipal: "someone",
		},
		TlsInfo: &adminv2.TLSInfo{
			Enabled: true,
		},
		ListenerName: "external",
		Source: &adminv2.Source{
			IpAddress: "4.2.2.1",
			Port:      49722,
		},
		ClientId:              "a-unique-client-id",
		ClientSoftwareName:    "some-library",
		ClientSoftwareVersion: "v0.0.1",
		GroupId:               "group-a",
		GroupInstanceId:       "group-instance",
		GroupMemberId:         "group-member",
		ApiVersions:           map[int32]int32{0: 4, 9: 11},
		IdleDuration:          durationpb.New(100 * time.Millisecond),
		TransactionalId:       "trans-id",
		InFlightRequests: &adminv2.InFlightRequests{
			SampledInFlightRequests: []*adminv2.InFlightRequests_Request{
				{
					ApiKey:           0, // PRODUCE
					InFlightDuration: durationpb.New(40 * time.Millisecond),
				},
			},
			HasMoreRequests: true,
		},
		TotalRequestStatistics: &adminv2.RequestStatistics{
			ProduceBytes:      10000,
			FetchBytes:        2000,
			RequestCount:      200,
			ProduceBatchCount: 10,
		},
		RecentRequestStatistics: &adminv2.RequestStatistics{
			ProduceBytes:      1000,
			FetchBytes:        200,
			RequestCount:      20,
			ProduceBatchCount: 1,
		},
	}

	out := parseKafkaConnection(protoConn)

	// Verify basic fields
	require.Equal(t, int32(2), out.NodeID)
	require.Equal(t, uint32(4), out.ShardID)
	require.Equal(t, "36338ca5-86b7-4478-ad23-32d49cfaef61", out.UID)
	require.Equal(t, "OPEN", out.State)
	require.Equal(t, "2025-10-23T01:02:03Z", out.OpenTime)
	require.Nil(t, out.CloseTime)
	require.NotEmpty(t, out.ConnectionDuration)
	require.True(t, out.TLSEnabled)
	require.Equal(t, "group-a", out.GroupID)
	require.Equal(t, "group-instance", out.GroupInstanceID)
	require.Equal(t, "group-member", out.GroupMemberID)
	require.Equal(t, "100ms", out.IdleDuration)
	require.Equal(t, "external", out.ListenerName)
	require.Equal(t, "trans-id", out.TransactionalID)

	// Verify authentication
	require.Equal(t, &KafkaConnectionAuthentication{
		State:         "SUCCESS",
		Mechanism:     "MTLS",
		UserPrincipal: "someone",
	}, out.Authentication)

	// Verify client
	require.Equal(t, &KafkaClient{
		IP:              "4.2.2.1",
		Port:            49722,
		ID:              "a-unique-client-id",
		SoftwareName:    "some-library",
		SoftwareVersion: "v0.0.1",
	}, out.Client)

	// Verify API versions (order-independent check)
	require.ElementsMatch(t, []KafkaAPIVersion{
		{API: "PRODUCE", Version: 4},
		{API: "OFFSET_FETCH", Version: 11},
	}, out.APIVersions)

	// Verify active requests
	require.Equal(t, &ActiveKafkaRequests{
		SampledRequests: []SampledKafkaRequest{
			{API: "PRODUCE", Duration: "40ms"},
		},
		HasMoreRequests: true,
	}, out.ActiveRequests)

	// Verify statistics
	require.Equal(t, &KafkaRequestStatistics{
		ProduceBytes:      10000,
		FetchBytes:        2000,
		RequestCount:      200,
		ProduceBatchCount: 10,
	}, out.RequestStatisticsAll)

	require.Equal(t, &KafkaRequestStatistics{
		ProduceBytes:      1000,
		FetchBytes:        200,
		RequestCount:      20,
		ProduceBatchCount: 1,
	}, out.RequestStatistics1m)
}

func TestGetConnectionDuration(t *testing.T) {
	now := time.Now().UTC()
	closed := timestamppb.New(now)
	opened := timestamppb.New(now.Add(-10 * time.Second))

	t.Run("open", func(t *testing.T) {
		require.Equal(t, "10s", getConnectionDuration(&adminv2.KafkaConnection{OpenTime: opened}))
	})

	t.Run("closed", func(t *testing.T) {
		require.Equal(t, "10s", getConnectionDuration(&adminv2.KafkaConnection{
			OpenTime:  opened,
			CloseTime: closed,
		}))
	})
}
