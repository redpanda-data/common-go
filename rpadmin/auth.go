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
	"fmt"
	"net/http"
)

// Auth affixes auth to an http request.
type Auth interface{ apply(req *http.Request) }

type (
	// BasicAuth options struct.
	BasicAuth struct {
		Username string
		Password string
	}
	// BearerToken options struct.
	BearerToken struct {
		Token string
	}
	// NopAuth options struct.
	NopAuth struct{}
)

func (a *BasicAuth) apply(req *http.Request) {
	req.SetBasicAuth(a.Username, a.Password)
}

func (a *BearerToken) apply(req *http.Request) {
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", a.Token))
}

func (*NopAuth) apply(*http.Request) {}
