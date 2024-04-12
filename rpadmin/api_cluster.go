// Copyright 2021 Redpanda Data, Inc.
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
	"net/http"
)

// ClusterHealthOverview overview data structure.
type ClusterHealthOverview struct {
	IsHealthy                 bool     `json:"is_healthy"`
	UnhealthyReasons          []string `json:"unhealthy_reasons"`
	ControllerID              int      `json:"controller_id"`
	AllNodes                  []int    `json:"all_nodes"`
	NodesDown                 []int    `json:"nodes_down"`
	NodesInRecoveryMode       []int    `json:"nodes_in_recovery_mode,omitempty"` // This is nil if not-supported or no nodes in recovery mode.
	LeaderlessPartitions      []string `json:"leaderless_partitions"`
	LeaderlessCount           *int     `json:"leaderless_count,omitempty"`
	UnderReplicatedPartitions []string `json:"under_replicated_partitions"`
	UnderReplicatedCount      *int     `json:"under_replicated_count,omitempty"`
}

// PartitionBalancerStatus is the status of the partition auto balancer.
type PartitionBalancerStatus struct {
	// Status is either off, ready, starting, in_progress or stalled.
	//
	//   off:          The balancer is disabled.
	//   ready:        The balancer is active but there is nothing to do.
	//   starting:     The balancer is starting but has not run yet.
	//   in_progress:  The balancer is active and is in the process of
	//                 scheduling partition movements.
	//   stalled:      There are some violations, but for some reason, the
	//                 balancer cannot make progress in mitigating them.
	Status string `json:"status,omitempty"`
	// Violations are the partition balancer violations.
	Violations PartitionBalancerViolations `json:"violations,omitempty"`
	// SecondsSinceLastTick is the last time the partition balancer ran.
	SecondsSinceLastTick int `json:"seconds_since_last_tick,omitempty"`
	// CurrentReassignmentsCount is the current number of partition
	// reassignments in progress.
	CurrentReassignmentsCount int `json:"current_reassignments_count,omitempty"`
	// PartitionsPendingForceRecovery Specifies the number of partitions that
	// are yet to be force recovered. This is a pointer because not all Redpanda
	// versions include this parameter.
	PartitionsPendingForceRecovery *int `json:"partitions_pending_force_recovery_count"`
	// PartitionsPendingRecoveryList Is a sample list of partitions pending
	// force recovery (limit capped to 10).
	PartitionsPendingRecoveryList []string `json:"partitions_pending_force_recovery_sample"`
}

// PartitionBalancerViolations describe the violations of the partition
// auto balancer.
type PartitionBalancerViolations struct {
	// UnavailableNodes are the nodes that have been unavailable after a time
	// set by 'partition_autobalancing_node_availability_timeout_sec' property.
	UnavailableNodes []int `json:"unavailable_nodes,omitempty"`
	// OverDiskLimitNodes are the nodes that surpassed the threshold of used
	// disk percentage set by 'partition_autobalancing_max_disk_usage_percent'
	// property.
	OverDiskLimitNodes []int `json:"over_disk_limit_nodes,omitempty"`
}

// PartitionsMovementResult is the information of the partitions movements that
// were canceled.
type PartitionsMovementResult struct {
	Namespace string `json:"ns,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Partition int    `json:"partition,omitempty"`
	Result    string `json:"result,omitempty"`
}

// ClusterUUID holds the cluster UUID.
type ClusterUUID struct {
	UUID string `json:"cluster_uuid"`
}

// ClusterView represents a cluster view as seen by one node. There are
// many keys returned, so the raw response is just unmarshalled into an
// interface.
type ClusterView map[string]any

// GetHealthOverview gets the cluster health overview.
func (a *AdminAPI) GetHealthOverview(ctx context.Context) (ClusterHealthOverview, error) {
	var response ClusterHealthOverview
	return response, a.sendAny(ctx, http.MethodGet, "/v1/cluster/health_overview", nil, &response)
}

// GetPartitionStatus gets the cluster partition status.
func (a *AdminAPI) GetPartitionStatus(ctx context.Context) (PartitionBalancerStatus, error) {
	var response PartitionBalancerStatus
	return response, a.sendAny(ctx, http.MethodGet, "/v1/cluster/partition_balancer/status", nil, &response)
}

// CancelAllPartitionsMovement cancels all partition movement.
func (a *AdminAPI) CancelAllPartitionsMovement(ctx context.Context) ([]PartitionsMovementResult, error) {
	var response []PartitionsMovementResult
	return response, a.sendAny(ctx, http.MethodPost, "/v1/cluster/cancel_reconfigurations", nil, &response)
}

// ClusterView returns a node view of the cluster.
func (a *AdminAPI) ClusterView(ctx context.Context) (ClusterView, error) {
	var response ClusterView
	return response, a.sendOne(ctx, http.MethodGet, "/v1/cluster_view", nil, &response, true)
}

// ClusterUUID returns the UUID of the cluster this node belongs to. Not to be
// confused with the configurable cluster identifier used in metrics.
func (a *AdminAPI) ClusterUUID(ctx context.Context) (ClusterUUID, error) {
	var response ClusterUUID
	return response, a.sendToLeader(ctx, http.MethodGet, "/v1/cluster/uuid", nil, &response)
}
