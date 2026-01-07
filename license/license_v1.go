// Copyright 2025 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package license

import (
	"slices"
	"time"
)

// LicenseType is the type for a v1 license represented by a string.
// In v1 licenses we have a few "well known" license types, but it
// is structured as a string for ease of expansion in the future without
// having to be a strongly-typed enum.
type LicenseType string

const (
	// LicenseTypeOpenSource describes an open source license, currently a virtual
	// license type as it represents no license at all.
	LicenseTypeOpenSource LicenseType = "open_source"
	// LicenseTypeEnterprise represents an enterprise license, whether expired or
	// currently valid.
	LicenseTypeEnterprise LicenseType = "enterprise"
	// LicenseTypeFreeTrial represents a trial license automatically initialized
	// when a cluster is initialized without an enterprise license.
	LicenseTypeFreeTrial LicenseType = "free_trial"
)

// V1RedpandaLicense is the payload that will be decoded from a license file.
type V1RedpandaLicense struct {
	Version      int    `json:"version"`
	Organization string `json:"org"`

	// Type of the license
	Type LicenseType `json:"type"`

	// Unix epoch
	Expiry int64 `json:"expiry"`

	// Products that are assigned by this license
	Products []Product `json:"products"`

	// SHA-256 hash of the raw bytes
	Checksum string `json:"-"`
}

// AllowsEnterpriseFeatures returns true if license type allows enterprise features.
func (r *V1RedpandaLicense) AllowsEnterpriseFeatures() bool {
	// first check our expiration time
	if CheckExpiration(r.Expires()) != nil {
		return false
	}

	// Right now any enterprise or trial license that was valid when we started
	// is considered valid here.
	return r.Type == LicenseTypeEnterprise || r.Type == LicenseTypeFreeTrial
}

// Expires returns the underlying expiration time of the license.
func (r *V1RedpandaLicense) Expires() time.Time {
	return time.Unix(r.Expiry, 0)
}

// IncludesProduct returns whether or not the license is valid for the given product.
func (r *V1RedpandaLicense) IncludesProduct(product Product) bool {
	return slices.Contains(r.Products, product)
}
