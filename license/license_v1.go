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
	"fmt"
	"time"
)

// Product is a product for which a license is valid.
type Product string

const (
	// add known products here, though we do no validaiton that
	// the license only contains product references of these types

	// ProductConnect represents the connect product.
	ProductConnect Product = "CONNECT"
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
	if r.CheckExpiry() != nil {
		return false
	}

	// Right now any enterprise or trial license that was valid when we started
	// is considered valid here.
	return r.Type == LicenseTypeEnterprise || r.Type == LicenseTypeFreeTrial
}

// CheckExpiry returns nil if the license is still valid (not expired). Otherwise,
// it will return an error that provides context when the license expired.
func (r *V1RedpandaLicense) CheckExpiry() error {
	expires := time.Unix(r.Expiry, 0)
	if expires.Before(time.Now().UTC()) {
		return fmt.Errorf("license expired on %q", expires.Format(time.RFC3339))
	}
	return nil
}
