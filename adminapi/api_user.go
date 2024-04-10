// Copyright 2021 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package adminapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

const usersEndpoint = "/v1/security/users"

type newUser struct {
	User      string `json:"username,omitempty"`
	Password  string `json:"password"`
	Algorithm string `json:"algorithm"`
}

const (
	// ScramSha256 is the constant for SCRAM-SHA-256.
	ScramSha256 = "SCRAM-SHA-256"
	// ScramSha512 is the constant for SCRAM-SHA-512.
	ScramSha512 = "SCRAM-SHA-512"
	// CloudOIDC is the constant for CLOUD-OIDC.
	CloudOIDC = "CLOUD-OIDC"
)

// CreateUser creates a user with the given username and password using the
// given mechanism (SCRAM-SHA-256, SCRAM-SHA-512).
func (a *AdminAPI) CreateUser(ctx context.Context, username, password, mechanism string) error {
	if username == "" {
		return errors.New("invalid empty username")
	}
	if password == "" {
		return errors.New("invalid empty password")
	}
	u := newUser{
		User:      username,
		Password:  password,
		Algorithm: mechanism,
	}
	return a.sendToLeader(ctx, http.MethodPost, usersEndpoint, u, nil)
}

// UpdateUser updates a user with the given username and password using the
// given mechanism (SCRAM-SHA-256, SCRAM-SHA-512). The api call will error out if no default mechanism given.
func (a *AdminAPI) UpdateUser(ctx context.Context, username, password, mechanism string) error {
	if username == "" {
		return errors.New("invalid empty username")
	}
	if password == "" {
		return errors.New("invalid empty password")
	}

	if mechanism != ScramSha256 && mechanism != ScramSha512 {
		return fmt.Errorf("invalid mechanism, should either %q or %q", ScramSha256, ScramSha512)
	}
	u := newUser{
		Password:  password,
		Algorithm: mechanism,
	}
	// This is because the api endpoint is userEndpoint/{user}.
	path := usersEndpoint + "/" + url.PathEscape(username)
	return a.sendToLeader(ctx, http.MethodPut, path, u, nil)
}

// DeleteUser deletes the given username, if it exists.
func (a *AdminAPI) DeleteUser(ctx context.Context, username string) error {
	if username == "" {
		return errors.New("invalid empty username")
	}
	// This is because the api endpoint is userEndpoint/{user}.
	path := usersEndpoint + "/" + url.PathEscape(username)
	return a.sendToLeader(ctx, http.MethodDelete, path, nil, nil)
}

// ListUsers returns the current users.
func (a *AdminAPI) ListUsers(ctx context.Context) ([]string, error) {
	var users []string
	return users, a.sendAny(ctx, http.MethodGet, usersEndpoint, nil, &users)
}
