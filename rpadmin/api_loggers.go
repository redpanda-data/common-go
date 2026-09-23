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

// Logger is a registered logger and, when requested, its current log level.
type Logger struct {
	Name  string `json:"name"`
	Level string `json:"level,omitempty"`
}

// Loggers returns the registered loggers of the target node and their current
// log levels.
func (a *AdminAPI) Loggers(ctx context.Context) ([]Logger, error) {
	var response []Logger
	return response, a.sendAny(ctx, http.MethodGet, "/v1/loggers?include-levels=true", nil, &response)
}
