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

type V0LicenseType int

const (
	V0LicenseTypeOpenSource V0LicenseType = iota - 1
	V0LicenseTypeFreeTrial
	V0LicenseTypeEnterprise
)

var (
	licenseTypeStringsV0 = map[V0LicenseType]string{
		V0LicenseTypeOpenSource: "open source",
		V0LicenseTypeEnterprise: "enterprise",
		V0LicenseTypeFreeTrial:  "free trial",
	}
	stringsToLicenseTypeV0 = map[string]V0LicenseType{}
	OpenSourceLicense      = &V0RedpandaLicense{
		Type:   V0LicenseTypeOpenSource,
		Expiry: time.Now().Add(time.Hour * 24 * 365 * 10).Unix(),
	}
)

func init() {
	for license, description := range licenseTypeStringsV0 {
		stringsToLicenseTypeV0[description] = license
	}
}

func (t V0LicenseType) String() string {
	if description, ok := licenseTypeStringsV0[t]; ok {
		return description
	}
	// default to open source license
	return licenseTypeStringsV0[V0LicenseTypeOpenSource]
}

func V0LicenseTypeFromString(desc string) V0LicenseType {
	if license, ok := stringsToLicenseTypeV0[desc]; ok {
		return license
	}
	// default to open source license
	return V0LicenseTypeOpenSource
}

// V0RedpandaLicense is the payload that will be decoded from a license file.
type V0RedpandaLicense struct {
	Version      int    `json:"version"`
	Organization string `json:"org"`

	// 0 = FreeTrial; 1 = Enterprise
	Type V0LicenseType `json:"type"`

	// Unix epoch
	Expiry int64 `json:"expiry"`

	// SHA-256 hash of the raw bytes
	Checksum string `json:"-"`
}

// AllowsEnterpriseFeatures returns true if license type allows enterprise features.
func (r *V0RedpandaLicense) AllowsEnterpriseFeatures() bool {
	// first check our expiration time
	if r.CheckExpiry() != nil {
		return false
	}

	// Right now any enterprise or trial license that was valid when we started
	// is considered valid here.
	return r.Type == V0LicenseTypeEnterprise || r.Type == V0LicenseTypeFreeTrial
}

// CheckExpiry returns nil if the license is still valid (not expired). Otherwise,
// it will return an error that provides context when the license expired.
func (r *V0RedpandaLicense) CheckExpiry() error {
	expires := time.Unix(r.Expiry, 0)
	if expires.Before(time.Now().UTC()) {
		return fmt.Errorf("license expired on %q", expires.Format(time.RFC3339))
	}
	return nil
}
