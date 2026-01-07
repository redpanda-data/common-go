// Copyright 2026 Redpanda Data, Inc.
//
// Licensed as a Redpanda Enterprise file under the Redpanda Community
// License (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
// https://github.com/redpanda-data/common-go/blob/main/licenses/rcl.md

package license

import (
	"time"
)

// V0LicenseType is a type from a V0 license, represented by an integer
// enum.
type V0LicenseType int

const (
	// V0LicenseTypeOpenSource describes an open source license, currently a virtual
	// license type as it represents no license at all.
	V0LicenseTypeOpenSource V0LicenseType = iota - 1
	// V0LicenseTypeFreeTrial represents a trial license automatically initialized
	// when a cluster is initialized without an enterprise license.
	V0LicenseTypeFreeTrial
	// V0LicenseTypeEnterprise represents an enterprise license, whether expired or
	// currently valid.
	V0LicenseTypeEnterprise
)

var (
	licenseTypeStringsV0 = map[V0LicenseType]string{
		V0LicenseTypeOpenSource: "open source",
		V0LicenseTypeEnterprise: "enterprise",
		V0LicenseTypeFreeTrial:  "free trial",
	}
	// OpenSourceLicense is the fallback license for when a license
	// cannot be parsed or validated.
	OpenSourceLicense = &V0RedpandaLicense{
		Type:   V0LicenseTypeOpenSource,
		Expiry: time.Now().Add(time.Hour * 24 * 365 * 10).Unix(),
	}
)

func (t V0LicenseType) String() string {
	if description, ok := licenseTypeStringsV0[t]; ok {
		return description
	}
	// default to open source license
	return licenseTypeStringsV0[V0LicenseTypeOpenSource]
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
	if CheckExpiration(r.Expires()) != nil {
		return false
	}

	// Right now any enterprise or trial license that was valid when we started
	// is considered valid here.
	return r.Type == V0LicenseTypeEnterprise || r.Type == V0LicenseTypeFreeTrial
}

// Expires returns the underlying expiration time of the license.
func (r *V0RedpandaLicense) Expires() time.Time {
	return time.Unix(r.Expiry, 0)
}

// IncludesProduct returns whether or not the license is valid for the given product.
func (*V0RedpandaLicense) IncludesProduct(_ Product) bool {
	return true
}
