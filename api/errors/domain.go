// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package errors

// Domain is a type definition for specifying the error domain which is required
// in error details.
type Domain string

const (
	// DomainDataplane defines the string for the proto error domain "dataplane".
	DomainDataplane Domain = "redpanda.com/dataplane"
	// DomainControlplane defines the string for the proto error domain "controlplane".
	DomainControlplane Domain = "redpanda.com/controlplane"
)
