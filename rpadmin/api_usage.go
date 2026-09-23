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
	"net/http"
)

// DatalakeTopicUsage is per-topic datalake usage within a usage window.
type DatalakeTopicUsage struct {
	TopicName           string `json:"topic_name"`
	TopicRevision       int64  `json:"topic_revision"`
	KafkaBytesProcessed int64  `json:"kafka_bytes_processed"`
}

// DatalakeUsage is the datalake usage within a usage window. Topics is set when
// per-topic stats are available; otherwise MissingReason explains why they are
// absent.
type DatalakeUsage struct {
	Topics        []DatalakeTopicUsage `json:"topics,omitempty"`
	MissingReason string               `json:"missing_reason,omitempty"`
}

// UsageResponse is a single usage/metering window.
type UsageResponse struct {
	BeginTimestamp          int64         `json:"begin_timestamp"`
	EndTimestamp            int64         `json:"end_timestamp"`
	Open                    bool          `json:"open"`
	KafkaBytesReceivedCount int64         `json:"kafka_bytes_received_count"`
	KafkaBytesSentCount     int64         `json:"kafka_bytes_sent_count"`
	CloudStorageBytesGauge  int64         `json:"cloud_storage_bytes_gauge"` // -1 when unavailable.
	DatalakeUsage           DatalakeUsage `json:"datalake_usage"`
}

// Usage returns the usage/metering windows recorded by the target node. It
// requires the enable_usage cluster config to be set; otherwise the node
// responds with an error.
func (a *AdminAPI) Usage(ctx context.Context) ([]UsageResponse, error) {
	var response []UsageResponse
	return response, a.sendAny(ctx, http.MethodGet, "/v1/usage", nil, &response)
}
