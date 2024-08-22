// Copyright 2024 Redpanda Data, Inc.
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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddMigration(t *testing.T) {
	type testCase struct {
		name     string
		testFn   func(t *testing.T) http.HandlerFunc
		input    any
		expID    int
		expError bool
	}

	successfulAddResponse := AddMigrationResponse{
		ID: 123,
	}

	runTest := func(t *testing.T, test testCase) {
		server := httptest.NewServer(test.testFn(t))
		defer server.Close()

		client, err := NewAdminAPI([]string{server.URL}, new(NopAuth), nil)
		assert.NoError(t, err)

		id, err := client.AddMigration(context.Background(), test.input)

		if test.expError {
			assert.Error(t, err)
			assert.Equal(t, test.expID, id) // should be -1 for error
		} else {
			assert.NoError(t, err)
			assert.Equal(t, test.expID, id)
		}
	}

	tests := []testCase{
		{
			name: "should add outbound migration successfully",
			testFn: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/v1/migrations/", r.URL.Path)
					assert.Equal(t, http.MethodPut, r.Method)

					var migration OutboundMigration
					err := json.NewDecoder(r.Body).Decode(&migration)
					assert.NoError(t, err)
					assert.Equal(t, "outbound", migration.MigrationType)

					w.WriteHeader(http.StatusOK)
					resp, err := json.Marshal(successfulAddResponse)
					assert.NoError(t, err)
					w.Write(resp)
				}
			},
			input: OutboundMigration{
				MigrationType:  "outbound",
				Topics:         []Topic{{Topic: "test-topic"}},
				ConsumerGroups: []string{"test-group"},
			},
			expID: 123,
		},
		{
			name: "should add inbound migration successfully",
			testFn: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/v1/migrations/", r.URL.Path)
					assert.Equal(t, http.MethodPut, r.Method)

					var migration InboundMigration
					err := json.NewDecoder(r.Body).Decode(&migration)
					assert.NoError(t, err)
					assert.Equal(t, "inbound", migration.MigrationType)

					w.WriteHeader(http.StatusOK)
					resp, err := json.Marshal(successfulAddResponse)
					assert.NoError(t, err)
					w.Write(resp)
				}
			},
			input: InboundMigration{
				MigrationType:  "inbound",
				Topics:         []InboundTopic{{SourceTopic: Topic{Topic: "test-topic"}}},
				ConsumerGroups: []string{"test-group"},
			},
			expID: 123,
		},
		{
			name: "should return error for invalid migration type",
			testFn: func(t *testing.T) http.HandlerFunc {
				return func(_ http.ResponseWriter, _ *http.Request) {
					t.Fatal("Server should not be called for invalid migration type")
				}
			},
			input:    struct{ MigrationType string }{MigrationType: "invalid"},
			expError: true,
		},
		{
			name: "should not panic on nil response with error",
			testFn: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Internal Server Error"))
				}
			},
			input: OutboundMigration{
				MigrationType:  "outbound",
				Topics:         []Topic{{Topic: "test-topic"}},
				ConsumerGroups: []string{"test-group"},
			},
			expError: true,
			expID:    -1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runTest(t, test)
		})
	}
}

func TestExecuteMigration(t *testing.T) {
	type testCase struct {
		name     string
		testFn   func(t *testing.T) http.HandlerFunc
		id       int
		action   string
		expError bool
	}

	runTest := func(t *testing.T, test testCase) {
		server := httptest.NewServer(test.testFn(t))
		defer server.Close()

		client, err := NewAdminAPI([]string{server.URL}, new(NopAuth), nil)
		assert.NoError(t, err)

		err = client.ExecuteMigration(context.Background(), test.id, test.action)

		if test.expError {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}

	tests := []testCase{
		{
			name: "should execute migration action successfully",
			testFn: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/v1/migrations/123", r.URL.Path)
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, "prepare", r.URL.Query().Get("action"))

					w.WriteHeader(http.StatusOK)
				}
			},
			id:     123,
			action: "prepare",
		},
		{
			name: "should return error for invalid action",
			testFn: func(t *testing.T) http.HandlerFunc {
				return func(_ http.ResponseWriter, _ *http.Request) {
					t.Fatal("Server should not be called for invalid action")
				}
			},
			id:       123,
			action:   "invalid",
			expError: true,
		},
		{
			name: "should handle server error",
			testFn: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Internal Server Error"))
				}
			},
			id:       123,
			action:   "execute",
			expError: true,
		},
		{
			name: "should handle all valid actions",
			testFn: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					action := r.URL.Query().Get("action")
					assert.Contains(t, []string{"prepare", "execute", "finish", "cancel"}, action)
					w.WriteHeader(http.StatusOK)
				}
			},
			id:     123,
			action: "finish", // We'll test one of the valid actions here
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runTest(t, test)
		})
	}
}
