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

// FeatureState enumerates the possible states of a feature.
type FeatureState string

const (
	FeatureStateActive      FeatureState = "active"      // FeatureStateActive is the active state.
	FeatureStatePreparing   FeatureState = "preparing"   // FeatureStatePreparing is the preparing state.
	FeatureStateAvailable   FeatureState = "available"   // FeatureStateAvailable is the available state.
	FeatureStateUnavailable FeatureState = "unavailable" // FeatureStateUnavailable is the unavailable state.
	FeatureStateDisabled    FeatureState = "disabled"    // FeatureStateDisabled is the disabled state.
)

// Feature contains information on the state of a feature.
type Feature struct {
	Name      string       `json:"name"`
	State     FeatureState `json:"state"`
	WasActive bool         `json:"was_active"`
}

// FeaturesResponse contains information on the features available on a Redpanda cluster.
type FeaturesResponse struct {
	ClusterVersion int       `json:"cluster_version"`
	Features       []Feature `json:"features"`
}

// License holds license data.
type License struct {
	Loaded     bool              `json:"loaded"`
	Properties LicenseProperties `json:"license"`
}

// LicenseProperties holds license properties.
type LicenseProperties struct {
	Version      int    `json:"format_version"`
	Organization string `json:"org"`
	Type         string `json:"type"`
	Expires      int64  `json:"expires"`
	Checksum     string `json:"sha256"`
}

// GetFeatures returns information about the available features.
func (a *AdminAPI) GetFeatures(ctx context.Context) (FeaturesResponse, error) {
	var features FeaturesResponse
	return features, a.sendToLeader(
		ctx,
		http.MethodGet,
		"/v1/features",
		nil,
		&features)
}

// GetLicenseInfo gets the license info.
func (a *AdminAPI) GetLicenseInfo(ctx context.Context) (License, error) {
	var license License
	return license, a.sendToLeader(ctx, http.MethodGet, "/v1/features/license", nil, &license)
}

// SetLicense sets the license.
func (a *AdminAPI) SetLicense(ctx context.Context, license any) error {
	return a.sendToLeader(ctx, http.MethodPut, "/v1/features/license", license, nil)
}
