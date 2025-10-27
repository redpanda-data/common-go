// Copyright 2025 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package mappers attempts to build a mapping between adminv2 and a common format for use in rpk and console
package mappers

import (
	"strings"
	"time"

	adminv2 "buf.build/gen/go/redpandadata/core/protocolbuffers/go/redpanda/core/admin/v2"
)

// KafkaConnection represents a Kafka connection with custom formatting for output.
type KafkaConnection struct {
	NodeID               int32                          `json:"node_id" yaml:"node_id"`
	ShardID              uint32                         `json:"shard_id" yaml:"shard_id"`
	UID                  string                         `json:"uid" yaml:"uid"`
	State                string                         `json:"state" yaml:"state"`
	OpenTime             string                         `json:"open_time" yaml:"open_time"`
	CloseTime            *string                        `json:"close_time" yaml:"close_time"`
	ConnectionDuration   string                         `json:"connection_duration" yaml:"connection_duration"`
	Authentication       *KafkaConnectionAuthentication `json:"authentication,omitempty" yaml:"authentication,omitempty"`
	TLSEnabled           bool                           `json:"tls_enabled" yaml:"tls_enabled"`
	Client               *KafkaClient                   `json:"client,omitempty" yaml:"client,omitempty"`
	GroupID              string                         `json:"group_id,omitempty" yaml:"group_id,omitempty"`
	GroupInstanceID      string                         `json:"group_instance_id,omitempty" yaml:"group_instance_id,omitempty"`
	GroupMemberID        string                         `json:"group_member_id,omitempty" yaml:"group_member_id,omitempty"`
	APIVersions          []KafkaAPIVersion              `json:"api_versions" yaml:"api_versions"`
	IdleDuration         string                         `json:"idle_duration,omitempty" yaml:"idle_duration,omitempty"`
	ListenerName         string                         `json:"listener_name,omitempty" yaml:"listener_name,omitempty"`
	TransactionalID      string                         `json:"transactional_id,omitempty" yaml:"transactional_id,omitempty"`
	ActiveRequests       *ActiveKafkaRequests           `json:"active_requests,omitempty" yaml:"active_requests,omitempty"`
	RequestStatisticsAll *KafkaRequestStatistics        `json:"request_statistics_all,omitempty" yaml:"request_statistics_all,omitempty"`
	RequestStatistics1m  *KafkaRequestStatistics        `json:"request_statistics_1m,omitempty" yaml:"request_statistics_1m,omitempty"`
}

// KafkaConnectionAuthentication holds authentication information.
type KafkaConnectionAuthentication struct {
	State         string `json:"state" yaml:"state"`
	Mechanism     string `json:"mechanism" yaml:"mechanism"`
	UserPrincipal string `json:"user_principal,omitempty" yaml:"user_principal,omitempty"`
}

// KafkaClient holds client connection information.
type KafkaClient struct {
	IP              string `json:"ip,omitempty" yaml:"ip,omitempty"`
	Port            uint32 `json:"port,omitempty" yaml:"port,omitempty"`
	ID              string `json:"id,omitempty" yaml:"id,omitempty"`
	SoftwareName    string `json:"software_name,omitempty" yaml:"software_name,omitempty"`
	SoftwareVersion string `json:"software_version,omitempty" yaml:"software_version,omitempty"`
}

// KafkaAPIVersion represents a Kafka API and its version.
type KafkaAPIVersion struct {
	API     string `json:"api" yaml:"api"`
	Version int32  `json:"version" yaml:"version"`
}

// ActiveKafkaRequests holds information about in-flight requests.
type ActiveKafkaRequests struct {
	SampledRequests []SampledKafkaRequest `json:"sampled_requests" yaml:"sampled_requests"`
	HasMoreRequests bool                  `json:"has_more_requests" yaml:"has_more_requests"`
}

// SampledKafkaRequest represents a single in-flight request.
type SampledKafkaRequest struct {
	API      string `json:"api" yaml:"api"`
	Duration string `json:"duration" yaml:"duration"`
}

// KafkaRequestStatistics holds aggregated request statistics.
type KafkaRequestStatistics struct {
	ProduceBytes      uint64 `json:"produce_bytes" yaml:"produce_bytes"`
	FetchBytes        uint64 `json:"fetch_bytes" yaml:"fetch_bytes"`
	RequestCount      uint64 `json:"request_count" yaml:"request_count"`
	ProduceBatchCount uint64 `json:"produce_batch_count" yaml:"produce_batch_count"`
}

func getConnectionDuration(conn *adminv2.KafkaConnection) string {
	opened := conn.OpenTime.AsTime()
	closed := conn.CloseTime.AsTime()

	duration := closed.Sub(opened)
	if opened.After(closed) {
		duration = time.Since(opened)
	}

	return duration.Round(time.Second).String()
}

func parseKafkaConnection(conn *adminv2.KafkaConnection) *KafkaConnection {
	c := &KafkaConnection{
		NodeID:  conn.NodeId,
		ShardID: conn.ShardId,
		UID:     conn.Uid,
		State:   strings.TrimPrefix(conn.State.String(), "KAFKA_CONNECTION_STATE_"),
	}

	// Add timestamps and duration
	if conn.OpenTime != nil {
		c.OpenTime = conn.OpenTime.AsTime().Format(time.RFC3339)
		if conn.CloseTime != nil {
			closeTime := conn.CloseTime.AsTime().Format(time.RFC3339)
			c.CloseTime = &closeTime
		}
		c.ConnectionDuration = getConnectionDuration(conn)
	}

	// Parse authentication info
	if conn.AuthenticationInfo != nil {
		c.Authentication = &KafkaConnectionAuthentication{
			State:         strings.TrimPrefix(conn.AuthenticationInfo.State.String(), "AUTHENTICATION_STATE_"),
			Mechanism:     strings.TrimPrefix(conn.AuthenticationInfo.Mechanism.String(), "AUTHENTICATION_MECHANISM_"),
			UserPrincipal: conn.AuthenticationInfo.UserPrincipal,
		}
	}

	c.TLSEnabled = conn.TlsInfo.Enabled

	// Parse client info
	if conn.Source != nil || conn.ClientId != "" || conn.ClientSoftwareName != "" || conn.ClientSoftwareVersion != "" {
		c.Client = &KafkaClient{
			ID:              conn.ClientId,
			SoftwareName:    conn.ClientSoftwareName,
			SoftwareVersion: conn.ClientSoftwareVersion,
		}
		if conn.Source != nil {
			c.Client.IP = conn.Source.IpAddress
			c.Client.Port = conn.Source.Port
		}
	}

	// Parse group ID
	c.GroupID = conn.GroupId
	c.GroupInstanceID = conn.GroupInstanceId
	c.GroupMemberID = conn.GroupMemberId

	// Parse API versions
	c.APIVersions = []KafkaAPIVersion{}
	for apiKey, version := range conn.ApiVersions {
		c.APIVersions = append(c.APIVersions, KafkaAPIVersion{
			API:     formatAPIKey(apiKey),
			Version: version,
		})
	}

	c.IdleDuration = conn.IdleDuration.AsDuration().String()
	c.ListenerName = conn.ListenerName
	c.TransactionalID = conn.TransactionalId

	// Parse in-flight requests
	c.ActiveRequests = &ActiveKafkaRequests{
		SampledRequests: []SampledKafkaRequest{},
		HasMoreRequests: conn.InFlightRequests.HasMoreRequests,
	}

	if conn.InFlightRequests != nil {
		if len(conn.InFlightRequests.SampledInFlightRequests) > 0 {
			c.ActiveRequests.SampledRequests = make([]SampledKafkaRequest, len(conn.InFlightRequests.SampledInFlightRequests))
			for i, req := range conn.InFlightRequests.SampledInFlightRequests {
				c.ActiveRequests.SampledRequests[i] = SampledKafkaRequest{
					API:      formatAPIKey(req.ApiKey),
					Duration: req.InFlightDuration.AsDuration().String(),
				}
			}
		}
	}

	// Parse total statistics
	if conn.TotalRequestStatistics != nil {
		c.RequestStatisticsAll = &KafkaRequestStatistics{
			ProduceBytes:      conn.TotalRequestStatistics.ProduceBytes,
			FetchBytes:        conn.TotalRequestStatistics.FetchBytes,
			RequestCount:      conn.TotalRequestStatistics.RequestCount,
			ProduceBatchCount: conn.TotalRequestStatistics.ProduceBatchCount,
		}
	}

	// Parse recent statistics
	if conn.RecentRequestStatistics != nil {
		c.RequestStatistics1m = &KafkaRequestStatistics{
			ProduceBytes:      conn.RecentRequestStatistics.ProduceBytes,
			FetchBytes:        conn.RecentRequestStatistics.FetchBytes,
			RequestCount:      conn.RecentRequestStatistics.RequestCount,
			ProduceBatchCount: conn.RecentRequestStatistics.ProduceBatchCount,
		}
	}

	return c
}

// formatAPIKey converts a Kafka API key to its string name.
func formatAPIKey(apiKey int32) string {
	// Kafka API keys: https://kafka.apache.org/protocol.html#protocol_api_keys
	names := map[int32]string{
		0:  "PRODUCE",
		1:  "FETCH",
		2:  "LIST_OFFSETS",
		3:  "METADATA",
		4:  "LEADER_AND_ISR",
		5:  "STOP_REPLICA",
		6:  "UPDATE_METADATA",
		7:  "CONTROLLED_SHUTDOWN",
		8:  "OFFSET_COMMIT",
		9:  "OFFSET_FETCH",
		10: "FIND_COORDINATOR",
		11: "JOIN_GROUP",
		12: "HEARTBEAT",
		13: "LEAVE_GROUP",
		14: "SYNC_GROUP",
		15: "DESCRIBE_GROUPS",
		16: "LIST_GROUPS",
		17: "SASL_HANDSHAKE",
		18: "API_VERSIONS",
		19: "CREATE_TOPICS",
		20: "DELETE_TOPICS",
		21: "DELETE_RECORDS",
		22: "INIT_PRODUCER_ID",
		23: "OFFSET_FOR_LEADER_EPOCH",
		24: "ADD_PARTITIONS_TO_TXN",
		25: "ADD_OFFSETS_TO_TXN",
		26: "END_TXN",
		27: "WRITE_TXN_MARKERS",
		28: "TXN_OFFSET_COMMIT",
		29: "DESCRIBE_ACLS",
		30: "CREATE_ACLS",
		31: "DELETE_ACLS",
		32: "DESCRIBE_CONFIGS",
		33: "ALTER_CONFIGS",
		34: "ALTER_REPLICA_LOG_DIRS",
		35: "DESCRIBE_LOG_DIRS",
		36: "SASL_AUTHENTICATE",
		37: "CREATE_PARTITIONS",
		38: "CREATE_DELEGATION_TOKEN",
		39: "RENEW_DELEGATION_TOKEN",
		40: "EXPIRE_DELEGATION_TOKEN",
		41: "DESCRIBE_DELEGATION_TOKEN",
		42: "DELETE_GROUPS",
		43: "ELECT_LEADERS",
		44: "INCREMENTAL_ALTER_CONFIGS",
		45: "ALTER_PARTITION_REASSIGNMENTS",
		46: "LIST_PARTITION_REASSIGNMENTS",
		47: "OFFSET_DELETE",
		48: "DESCRIBE_CLIENT_QUOTAS",
		49: "ALTER_CLIENT_QUOTAS",
		50: "DESCRIBE_USER_SCRAM_CREDENTIALS",
		51: "ALTER_USER_SCRAM_CREDENTIALS",
		52: "VOTE",
		53: "BEGIN_QUORUM_EPOCH",
		54: "END_QUORUM_EPOCH",
		55: "DESCRIBE_QUORUM",
		56: "ALTER_PARTITION",
		57: "UPDATE_FEATURES",
		58: "ENVELOPE",
		59: "FETCH_SNAPSHOT",
		60: "DESCRIBE_CLUSTER",
		61: "DESCRIBE_PRODUCERS",
		62: "BROKER_REGISTRATION",
		63: "BROKER_HEARTBEAT",
		64: "UNREGISTER_BROKER",
		65: "DESCRIBE_TRANSACTIONS",
		66: "LIST_TRANSACTIONS",
		67: "ALLOCATE_PRODUCER_IDS",
	}
	if name, ok := names[apiKey]; ok {
		return name
	}
	return "UNKNOWN"
}
