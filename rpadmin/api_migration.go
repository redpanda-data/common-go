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
	"fmt"
	"net/http"
	"reflect"
)

const (
	baseMigrationEndpoint = "/v1/migrations/"
)

// BucketConfiguration represents the configuration for a bucket
type BucketConfiguration struct {
	AccessKey           string `json:"access_key,omitempty"`
	SecretKey           string `json:"secret_key,omitempty"`
	Region              string `json:"region,omitempty"`
	Bucket              string `json:"bucket"`
	CredentialSource    string `json:"credential_source,omitempty"`
	TopicManifestPrefix string `json:"topic_manifest_prefix,omitempty"`
}

// OutboundMigration represents an outbound migration configuration
type OutboundMigration struct {
	MigrationType  string               `json:"migration_type"`
	Topics         []NamespacedTopic    `json:"topics,omitempty"`
	ConsumerGroups []string             `json:"consumer_groups,omitempty"`
	CopyTo         *BucketConfiguration `json:"copy_to,omitempty"`
	AutoAdvance    bool                 `json:"auto_advance,omitempty"`
}

// GetMigrationType gets the type of a migration
func (o *OutboundMigration) GetMigrationType() string {
	return o.MigrationType
}

// InboundMigration represents an inbound migration configuration
type InboundMigration struct {
	MigrationType  string         `json:"migration_type"`
	Topics         []InboundTopic `json:"topics,omitempty"`
	ConsumerGroups []string       `json:"consumer_groups,omitempty"`
	AutoAdvance    bool           `json:"auto_advance,omitempty"`
}

// GetMigrationType gets the type of a migration
func (o *InboundMigration) GetMigrationType() string {
	return o.MigrationType
}

// AddMigration adds a migration to the cluster. It accepts one of InboundMigration or OutboundMigration.
func (a *AdminAPI) addMigration(ctx context.Context, migration any) (AddMigrationResponse, error) {
	migrationType := reflect.TypeOf(migration)
	if migrationType != reflect.TypeOf(InboundMigration{}) && migrationType != reflect.TypeOf(OutboundMigration{}) {
		return AddMigrationResponse{}, fmt.Errorf("invalid migration type: must be either InboundMigration or OutboundMigration")
	}

	var response AddMigrationResponse
	if err := a.sendAny(ctx, http.MethodPut, baseMigrationEndpoint, migration, &response); err != nil {
		return AddMigrationResponse{}, err
	}
	return response, nil
}

// AddInboundMigration adds an inbound migration to the cluster.
func (a *AdminAPI) AddInboundMigration(ctx context.Context, migration InboundMigration) (AddMigrationResponse, error) {
	migration.MigrationType = "inbound"
	return a.addMigration(ctx, migration)
}

// AddOutboundMigration adds an outbound migration to the cluster.
func (a *AdminAPI) AddOutboundMigration(ctx context.Context, migration OutboundMigration) (AddMigrationResponse, error) {
	migration.MigrationType = "outbound"
	return a.addMigration(ctx, migration)
}

// MigrationState represents the state of a migration. The end user is expected to request the type from Migration
// and then type assert it to either inbound or outbound
type MigrationState struct {
	ID        int                 `json:"id"`
	State     string              `json:"state"`
	Migration MigrationTypeGetter `json:"migration"`
}

// MigrationTypeGetter is an interface satisfied by InboundMigration and OutboundMigration
// It allows the user to quickly tell which is which
type MigrationTypeGetter interface {
	GetMigrationType() string
}

// used for delayed parsing of the migration field so that we can put it in the right container
type migrationStateRaw struct {
	ID        int             `json:"id"`
	State     string          `json:"state"`
	Migration json.RawMessage `json:"migration"`
}

// GetMigration parses the json.RawMessage to the appropriate type and then returns it.
// Rather than doing any tricky stuff we simply unmarshal to outbound, check the MigrationType field
// and if that's not right we unmarshal to inbound
func (m migrationStateRaw) GetMigration() (MigrationTypeGetter, error) {
	var outbound OutboundMigration
	if err := json.Unmarshal(m.Migration, &outbound); err != nil {
		return nil, err
	}

	if outbound.MigrationType == "outbound" {
		return &outbound, nil
	}

	var inbound InboundMigration
	if err := json.Unmarshal(m.Migration, &inbound); err != nil {
		return nil, err
	}
	return &inbound, nil
}

// GetMigration gets a migration by its ID and returns a MigrationState
func (a *AdminAPI) GetMigration(ctx context.Context, id int) (*MigrationState, error) {
	var resp []byte
	if err := a.sendAny(ctx, http.MethodGet, fmt.Sprintf("%s%d", baseMigrationEndpoint, id), nil, &resp); err != nil {
		return nil, err
	}

	var data migrationStateRaw
	if err := json.Unmarshal(resp, &data); err != nil {
		return nil, err
	}

	m, err := data.GetMigration()
	if err != nil {
		return nil, err
	}

	return &MigrationState{
		ID:        data.ID,
		State:     data.State,
		Migration: m,
	}, nil
}

// ListMigrations returns a list of all migrations in the cluster.
func (a *AdminAPI) ListMigrations(ctx context.Context) ([]MigrationState, error) {
	var resp []byte
	if err := a.sendAny(ctx, http.MethodGet, baseMigrationEndpoint, nil, &resp); err != nil {
		return nil, err
	}

	var data []migrationStateRaw
	if err := json.Unmarshal(resp, &data); err != nil {
		return nil, err
	}

	out := make([]MigrationState, 0, len(data))
	for _, v := range data {
		o, err := v.GetMigration()
		if err != nil {
			return nil, err
		}
		out = append(out, MigrationState{
			ID:        v.ID,
			State:     v.State,
			Migration: o,
		})
	}
	return out, nil
}

// DeleteMigration deletes a migration by its ID.
func (a *AdminAPI) DeleteMigration(ctx context.Context, id int) error {
	return a.sendAny(ctx, http.MethodDelete, fmt.Sprintf("%s%d", baseMigrationEndpoint, id), nil, nil)
}

// ExecuteMigration executes a specific action on a migration identified by its ID.
// We set all migrations to auto_advance = true so there's generally no reason to call this
func (a *AdminAPI) ExecuteMigration(ctx context.Context, id int, action MigrationAction) error {
	if action < MigrationActionPrepare || action > MigrationActionCancel {
		return fmt.Errorf("invalid action: %s. Must be one of: prepare, execute, finish, cancel", action)
	}
	return a.sendAny(ctx, http.MethodPost, fmt.Sprintf("%s%d?action=%s", baseMigrationEndpoint, id, action), nil, nil)
}

// AddMigrationResponse is the response from adding a migration
type AddMigrationResponse struct {
	ID int `json:"id"`
}

// MigrationAction enum
type MigrationAction int

const (
	// MigrationActionPrepare is the prepare migration action.
	MigrationActionPrepare MigrationAction = iota

	// MigrationActionExecute is the execute migration action.
	MigrationActionExecute

	// MigrationActionFinish is the finish migration action.
	MigrationActionFinish

	// MigrationActionCancel is the cancel migration action.
	MigrationActionCancel
)

func (a MigrationAction) String() string {
	switch a {
	case MigrationActionPrepare:
		return "prepare"
	case MigrationActionExecute:
		return "execute"
	case MigrationActionFinish:
		return "finish"
	case MigrationActionCancel:
		return "cancel"
	default:
		return ""
	}
}

// MigrationActionFromString converts a string to a MigrationAction.
func MigrationActionFromString(s string) (MigrationAction, error) {
	switch s {
	case "prepare":
		return MigrationActionPrepare, nil
	case "execute":
		return MigrationActionExecute, nil
	case "finish":
		return MigrationActionFinish, nil
	case "cancel":
		return MigrationActionCancel, nil
	default:
		return MigrationActionPrepare, fmt.Errorf("invalid migration action: %s. Must be one of: prepare, execute, finish, cancel", s)
	}
}

// MigrationStatus enum
type MigrationStatus int

const (
	// MigrationStatusPlanned is the planned migration status.
	MigrationStatusPlanned MigrationStatus = iota
	// MigrationStatusPrepared is the prepared migration status.
	MigrationStatusPrepared
	// MigrationStatusExecuted is the executed migration status.
	MigrationStatusExecuted
	// MigrationStatusFinished is the finished migration status.
	MigrationStatusFinished
)

func (s MigrationStatus) String() string {
	switch s {
	case MigrationStatusPlanned:
		return "planned"
	case MigrationStatusPrepared:
		return "prepared"
	case MigrationStatusExecuted:
		return "executed"
	case MigrationStatusFinished:
		return "finished"
	default:
		return ""
	}
}

// MigrationStatusFromString converts a string to a MigrationStatus.
func MigrationStatusFromString(s string) (MigrationStatus, error) {
	switch s {
	case "planned":
		return MigrationStatusPlanned, nil
	case "prepared":
		return MigrationStatusPrepared, nil
	case "executed":
		return MigrationStatusExecuted, nil
	case "finished":
		return MigrationStatusFinished, nil
	default:
		return MigrationStatusPlanned, fmt.Errorf("invalid migration status: %s. Must be one of: planned, prepared, executed, finished", s)
	}
}
