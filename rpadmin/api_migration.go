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
	"fmt"
	"net/http"
	"reflect"
)

const (
	baseMigrationEndpoint = "/v1/migrations/"
)

// AddMigration adds a migration to the cluster. It accepts one of InboundMigration or OutboundMigration.
func (a *AdminAPI) AddMigration(ctx context.Context, migration any) (int, error) {
	migrationType := reflect.TypeOf(migration)
	if migrationType != reflect.TypeOf(InboundMigration{}) && migrationType != reflect.TypeOf(OutboundMigration{}) {
		return 0, fmt.Errorf("invalid migration type: must be either InboundMigration or OutboundMigration")
	}

	var response AddMigrationResponse
	if err := a.sendOne(ctx, http.MethodPut, baseMigrationEndpoint, migration, &response, false); err != nil {
		return -1, err
	}
	return response.ID, nil
}

// GetMigration gets a migration by its ID.
func (a *AdminAPI) GetMigration(ctx context.Context, id int) (MigrationState, error) {
	var response MigrationState
	err := a.sendOne(ctx, http.MethodGet, fmt.Sprintf("baseMigrationEndpoint%d", id), nil, &response, false)
	return response, err
}

// ListMigrations returns a list of all migrations in the cluster.
func (a *AdminAPI) ListMigrations(ctx context.Context) ([]MigrationState, error) {
	var response []MigrationState
	err := a.sendAny(ctx, http.MethodGet, baseMigrationEndpoint, nil, &response)
	return response, err
}

// DeleteMigration deletes a migration by its ID.
func (a *AdminAPI) DeleteMigration(ctx context.Context, id int) error {
	return a.sendAny(ctx, http.MethodDelete, fmt.Sprintf("baseMigrationEndpoint%d", id), nil, nil)
}

// ExecuteMigration executes a specific action on a migration identified by its ID. The action must be one of:
// prepare, execute, finish, cancel.
func (a *AdminAPI) ExecuteMigration(ctx context.Context, id int, action string) error {
	validActions := map[string]bool{
		"prepare": true,
		"execute": true,
		"finish":  true,
		"cancel":  true,
	}
	if !validActions[action] {
		return fmt.Errorf("invalid action: %s. Must be one of: prepare, execute, finish, cancel", action)
	}
	return a.sendAny(ctx, http.MethodPost, fmt.Sprintf("%s%d?action=%s", baseMigrationEndpoint, id, action), nil, nil)
}

// OutboundMigration represents an outbound migration request
type OutboundMigration struct {
	MigrationType  string   `json:"migration_type"`
	Topics         []Topic  `json:"topics"`
	ConsumerGroups []string `json:"consumer_groups"`
}

// InboundMigration represents an inbound migration configuration
type InboundMigration struct {
	MigrationType  string         `json:"migration_type"`
	Topics         []InboundTopic `json:"topics"`
	ConsumerGroups []string       `json:"consumer_groups"`
}

// InboundTopic represents an inbound migration topic
type InboundTopic struct {
	SourceTopic Topic  `json:"source_topic"`
	Alias       *Topic `json:"alias,omitempty"`
	Location    string `json:"location,omitempty"`
}

// MigrationState represents the state of a migration
type MigrationState struct {
	ID        int       `json:"id"`
	State     string    `json:"state"`
	Migration Migration `json:"migration"`
}

// Migration represents a migration
type Migration struct {
	MigrationType string  `json:"migration_type"`
	Topics        []Topic `json:"topics"`
}

// Topic represents a namespaced topic
type Topic struct {
	Topic     string `json:"topic"`
	Namespace string `json:"ns"`
}

// AddMigrationResponse is the response from adding a migration
type AddMigrationResponse struct {
	ID int `json:"id"`
}
