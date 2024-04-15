// Copyright 2023 Redpanda Data, Inc.
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

// DiskStatInfo is the disk data returned by the /disk_stat endpoint of the
// admin API.
type DiskStatInfo map[string]any

// DiskCache gets the disk cache.
func (a *AdminAPI) DiskCache(ctx context.Context) (DiskStatInfo, error) {
	var response DiskStatInfo
	return response, a.sendOne(ctx, http.MethodGet, "/v1/debug/storage/disk_stat/cache", nil, &response, false)
}

// DiskData gets the disk data info.
func (a *AdminAPI) DiskData(ctx context.Context) (DiskStatInfo, error) {
	var response DiskStatInfo
	return response, a.sendOne(ctx, http.MethodGet, "/v1/debug/storage/disk_stat/data", nil, &response, false)
}
