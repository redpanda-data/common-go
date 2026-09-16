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

// ReadyStatus is the readiness status reported by a node.
type ReadyStatus struct {
	Status string `json:"status"` // "ready" or "booting".
}

// Ready returns the readiness status of the target node.
func (a *AdminAPI) Ready(ctx context.Context) (ReadyStatus, error) {
	var response ReadyStatus
	return response, a.sendAny(ctx, http.MethodGet, "/v1/status/ready", nil, &response)
}
