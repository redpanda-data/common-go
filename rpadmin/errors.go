// Copyright 2025 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package rpadmin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ErrNoAdminAPILeader happens when there's no leader for the Admin API.
var ErrNoAdminAPILeader = errors.New("no Admin API leader found")

// ErrNoSRVRecordsFound happens when we try to deduce Admin API URLs
// from Kubernetes SRV DNS records, but no records were returned by
// the DNS query.
var ErrNoSRVRecordsFound = errors.New("not SRV DNS records found")

// HTTPResponseError is the error response.
type HTTPResponseError struct {
	Method   string
	URL      string
	Response *http.Response
	Body     []byte
}

// GenericErrorBody is the JSON decodable body that is produced by generic error
// handling in the admin server when a seastar http exception is thrown.
type GenericErrorBody struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// DecodeGenericErrorBody decodes generic error body.
func (he HTTPResponseError) DecodeGenericErrorBody() (GenericErrorBody, error) {
	var resp GenericErrorBody
	err := json.Unmarshal(he.Body, &resp)
	return resp, err
}

// Error returns string representation of the error.
func (he HTTPResponseError) Error() string {
	return fmt.Sprintf("request %s %s failed: %s, body: %q\n",
		he.Method, he.URL, http.StatusText(he.Response.StatusCode), he.Body)
}
