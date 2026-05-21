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
	"fmt"
	"net/http"
	"sort"
)

const (
	brokersEndpoint          = "/v1/brokers"
	brokerEndpoint           = "/v1/brokers/%d"
	brokerUuidsEndpoint      = "/v1/broker_uuids"
	preRestartProbeEndpoint  = "/v1/broker/pre_restart_probe"
	postRestartProbeEndpoint = "/v1/broker/post_restart_probe"
)

// MaintenanceStatus is the maintenance status.
type MaintenanceStatus struct {
	Draining     bool  `json:"draining"`
	Finished     *bool `json:"finished"`
	Errors       *bool `json:"errors"`
	Partitions   *int  `json:"partitions"`
	Eligible     *int  `json:"eligible"`
	Transferring *int  `json:"transferring"`
	Failed       *int  `json:"failed"`
}

// MembershipStatus enumerates possible membership states for brokers.
type MembershipStatus string

const (
	// MembershipStatusActive indicates an active broker.
	MembershipStatusActive MembershipStatus = "active"
	// MembershipStatusDraining indicates that the broker is being drained, e.g. for decommission.
	MembershipStatusDraining MembershipStatus = "draining"
)

// Broker is the information returned from the Redpanda admin broker endpoints.
type Broker struct {
	NodeID             int                `json:"node_id"`
	NumCores           int                `json:"num_cores"`
	Rack               string             `json:"rack"`
	InternalRPCAddress string             `json:"internal_rpc_address"`
	InternalRPCPort    int                `json:"internal_rpc_port"`
	MembershipStatus   MembershipStatus   `json:"membership_status"`
	IsAlive            *bool              `json:"is_alive"`
	Version            string             `json:"version"`
	Maintenance        *MaintenanceStatus `json:"maintenance_status"`
	DiskSpace          []DiskSpace        `json:"disk_space"`
}

// DiskSpace disk space information per directory path.
type DiskSpace struct {
	Free  int    `json:"free"`
	Total int    `json:"total"`
	Path  string `json:"path"`
}

// DecommissionPartitions holds decommission partitions info.
type DecommissionPartitions struct {
	Ns              string               `json:"ns"`
	Topic           string               `json:"topic"`
	Partition       int                  `json:"partition"`
	MovingTo        DecommissionMovingTo `json:"moving_to"`
	BytesLeftToMove int                  `json:"bytes_left_to_move"`
	BytesMoved      int                  `json:"bytes_moved"`
	PartitionSize   int                  `json:"partition_size"`
}

// DecommissionMovingTo holds moving to info.
type DecommissionMovingTo struct {
	NodeID int `json:"node_id"`
	Core   int `json:"core"`
}

// ReallocationFailedPartition holds reallocation failed partition detail.
type ReallocationFailedPartition struct {
	NS        string `json:"ns"`
	Topic     string `json:"topic"`
	Partition int    `json:"partition"`
	Error     string `json:"error"`
}

// DecommissionStatusResponse is the response to DecommissionBrokerStatus.
type DecommissionStatusResponse struct {
	Finished                   bool                          `json:"finished"`
	ReplicasLeft               int                           `json:"replicas_left"`
	AllocationFailures         []string                      `json:"allocation_failures"`
	Partitions                 []DecommissionPartitions      `json:"partitions"`
	ReallocationFailureDetails []ReallocationFailedPartition `json:"reallocation_failure_details,omitempty"`
}

// BrokerUuids is information that shows the mapping of node ID to node UUID.
type BrokerUuids struct {
	NodeID int    `json:"node_id"`
	UUID   string `json:"uuid"`
}

// Brokers queries one of the client's hosts and returns the list of brokers.
func (a *AdminAPI) Brokers(ctx context.Context) ([]Broker, error) {
	var bs []Broker
	defer func() {
		sort.Slice(bs, func(i, j int) bool { return bs[i].NodeID < bs[j].NodeID })
	}()
	return bs, a.sendAny(ctx, http.MethodGet, brokersEndpoint, nil, &bs)
}

// Broker queries one of the client's hosts and returns broker information.
func (a *AdminAPI) Broker(ctx context.Context, node int) (Broker, error) {
	var b Broker
	err := a.sendAny(
		ctx,
		http.MethodGet,
		fmt.Sprintf(brokerEndpoint, node), nil, &b)
	return b, err
}

// DecommissionBroker issues a decommission request for the given broker.
func (a *AdminAPI) DecommissionBroker(ctx context.Context, node int) error {
	return a.sendToLeader(
		ctx,
		http.MethodPut,
		fmt.Sprintf("%s/%d/decommission", brokersEndpoint, node),
		nil,
		nil,
	)
}

// DecommissionBrokerStatus gathers a decommissioning progress for the given broker.
func (a *AdminAPI) DecommissionBrokerStatus(ctx context.Context, node int) (DecommissionStatusResponse, error) {
	var dsr DecommissionStatusResponse
	err := a.sendToLeader(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/%d/decommission", brokersEndpoint, node),
		nil,
		&dsr,
	)
	return dsr, err
}

// RecommissionBroker issues a recommission request for the given broker.
func (a *AdminAPI) RecommissionBroker(ctx context.Context, node int) error {
	return a.sendToLeader(
		ctx,
		http.MethodPut,
		fmt.Sprintf("%s/%d/recommission", brokersEndpoint, node),
		nil,
		nil,
	)
}

// EnableMaintenanceMode enables maintenance mode for a node.
func (a *AdminAPI) EnableMaintenanceMode(ctx context.Context, nodeID int) error {
	return a.sendAny(
		ctx,
		http.MethodPut,
		fmt.Sprintf("%s/%d/maintenance", brokersEndpoint, nodeID),
		nil,
		nil,
	)
}

// DisableMaintenanceMode disables maintenance mode for a node.
func (a *AdminAPI) DisableMaintenanceMode(ctx context.Context, nodeID int, useLeaderNode bool) error {
	if useLeaderNode {
		return a.sendToLeader(
			ctx,
			http.MethodDelete,
			fmt.Sprintf("%s/%d/maintenance", brokersEndpoint, nodeID),
			nil,
			nil,
		)
	}

	return a.sendAny(
		ctx,
		http.MethodDelete,
		fmt.Sprintf("%s/%d/maintenance", brokersEndpoint, nodeID),
		nil,
		nil,
	)
}

// MaintenanceStatus returns the maintenance status of a node.
func (a *AdminAPI) MaintenanceStatus(ctx context.Context) (MaintenanceStatus, error) {
	var response MaintenanceStatus
	return response, a.sendAny(ctx, http.MethodGet, "/v1/maintenance", nil, nil)
}

// CancelNodePartitionsMovement cancels node's partition movement.
func (a *AdminAPI) CancelNodePartitionsMovement(ctx context.Context, node int) ([]PartitionsMovementResult, error) {
	var response []PartitionsMovementResult
	return response, a.sendAny(ctx, http.MethodPost, fmt.Sprintf("%s/%d/cancel_partition_moves", brokersEndpoint, node), nil, &response)
}

// GetBrokerUuids retrieves the mapping of node ID to node UUID.
func (a *AdminAPI) GetBrokerUuids(ctx context.Context) ([]BrokerUuids, error) {
	var response []BrokerUuids
	return response, a.sendAny(ctx, http.MethodGet, brokerUuidsEndpoint, nil, &response)
}

// RestartRisks lists the partitions affected by restarting the local
// broker, arranged by risk type. Each list is truncated to the limit
// requested by the caller (default 128 partitions per category).
//
// See the Redpanda admin API spec for /v1/broker/pre_restart_probe
// (introduced in 25.1) for the authoritative definition of each
// category:
//
//	https://github.com/redpanda-data/redpanda/blob/dev/src/v/redpanda/admin/api-doc/broker.json
type RestartRisks struct {
	// RF1Offline are partitions in RF=1 topics whose only replica is
	// hosted on the local broker. Restarting the broker takes these
	// partitions offline for the duration of the restart — generally
	// acceptable risk, since RF=1 already implies no redundancy.
	RF1Offline []string `json:"rf1_offline"`
	// FullAcksProduceUnavailable are partitions that may reject produce
	// requests with acks=-1 if the local broker is restarted.
	FullAcksProduceUnavailable []string `json:"full_acks_produce_unavailable"`
	// Unavailable are partitions that may reject both consume and
	// produce requests if the local broker is restarted.
	Unavailable []string `json:"unavailable"`
	// Acks1DataLoss are partitions that may lose data produced with
	// acks=1 if the local broker is restarted.
	Acks1DataLoss []string `json:"acks1_data_loss"`
}

// PreRestartCheckResult is the response from the broker's pre-restart probe.
type PreRestartCheckResult struct {
	Risks RestartRisks `json:"risks"`
}

// PostRestartCheckResult is the response from the broker's post-restart probe.
type PostRestartCheckResult struct {
	// LoadReclaimedPercent measures the load the broker has reclaimed
	// after a restart as a percentage of in-sync replicas (0 to 100).
	// A value of 100 indicates the broker has fully caught up.
	LoadReclaimedPercent int `json:"load_reclaimed_pc"`
}

// PreRestartProbe queries the broker's pre-restart probe, which returns
// the partitions affected by restarting it grouped by risk type. The
// probe answers for the broker that handles the request, so callers
// generally scope the client to a specific broker (via ForHost) before
// invoking this method.
//
// limit caps the number of partitions returned per risk category; pass
// 0 to use the server-side default (128).
//
// Added in Redpanda 25.1. See the admin API spec for the authoritative
// definition of the response:
//
//	https://github.com/redpanda-data/redpanda/blob/dev/src/v/redpanda/admin/api-doc/broker.json
func (a *AdminAPI) PreRestartProbe(ctx context.Context, limit int) (PreRestartCheckResult, error) {
	path := preRestartProbeEndpoint
	if limit > 0 {
		path = fmt.Sprintf("%s?limit=%d", preRestartProbeEndpoint, limit)
	}
	var r PreRestartCheckResult
	return r, a.sendAny(ctx, http.MethodGet, path, nil, &r)
}

// PostRestartProbe queries the broker's post-restart probe, which reports
// how much load this broker has reclaimed since the most recent restart
// as a percentage of in-sync replicas. The probe answers for the broker
// that handles the request, so callers generally scope the client to a
// specific broker (via ForHost) before invoking this method.
//
// limit caps the number of partitions inspected by the probe; pass 0 to
// use the server-side default (128).
//
// Added in Redpanda 25.1. See the admin API spec for the authoritative
// definition of the response:
//
//	https://github.com/redpanda-data/redpanda/blob/dev/src/v/redpanda/admin/api-doc/broker.json
func (a *AdminAPI) PostRestartProbe(ctx context.Context, limit int) (PostRestartCheckResult, error) {
	path := postRestartProbeEndpoint
	if limit > 0 {
		path = fmt.Sprintf("%s?limit=%d", postRestartProbeEndpoint, limit)
	}
	var r PostRestartCheckResult
	return r, a.sendAny(ctx, http.MethodGet, path, nil, &r)
}
