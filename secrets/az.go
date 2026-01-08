// Copyright 2026 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const latestVersion = ""

type azSecretsManager struct {
	client *azsecrets.Client
	logger *slog.Logger
	tags   map[string]string
}

// NewAzSecretsManager creates a new Azure secrets manager client.
// The optional globalTags parameter specifies tags that will be applied to all secrets.
func NewAzSecretsManager(logger *slog.Logger, vaultURL string, globalTags ...map[string]string) (SecretAPI, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	client, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create secretmanager client: %w", err)
	}

	tags := make(map[string]string)
	if len(globalTags) > 0 && globalTags[0] != nil {
		for k, v := range globalTags[0] {
			tags[k] = v
		}
	}

	return &azSecretsManager{
		client: client,
		logger: logger,
		tags:   tags,
	}, nil
}

// GetSecretValue gets a secret value.
func (a *azSecretsManager) GetSecretValue(ctx context.Context, key string) (string, bool) {
	key = sanitize(key)
	resp, err := a.client.GetSecret(ctx, key, latestVersion, nil)
	if err != nil {
		if status.Code(err) != codes.NotFound {
			a.logger.With("error", err, "key", key).Error("Failed to look up secret")
		}
		return "", false
	}

	return *resp.Value, true
}

// CheckSecretExists checks if a secret exists.
func (a *azSecretsManager) CheckSecretExists(ctx context.Context, key string) bool {
	key = sanitize(key)
	pager := a.client.NewListSecretVersionsPager(key, nil)
	if !pager.More() {
		return false
	}

	page, err := pager.NextPage(ctx)
	return err == nil && len(page.Value) > 0
}

// CreateSecret creates a new secret.
func (a *azSecretsManager) CreateSecret(ctx context.Context, key string, value string, tags map[string]string) error {
	key = sanitize(key)
	mergedTags := a.mergeTags(tags)

	_, err := a.client.SetSecret(ctx, key, azsecrets.SetSecretParameters{
		Value: &value,
		Tags:  mergedTags,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}
	return nil
}

// UpdateSecret updates an existing secret.
func (a *azSecretsManager) UpdateSecret(ctx context.Context, key string, value string, tags map[string]string) error {
	key = sanitize(key)
	mergedTags := a.mergeTags(tags)

	_, err := a.client.SetSecret(ctx, key, azsecrets.SetSecretParameters{
		Value: &value,
		Tags:  mergedTags,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}
	return nil
}

// DeleteSecret deletes a secret.
func (a *azSecretsManager) DeleteSecret(ctx context.Context, key string) error {
	key = sanitize(key)
	_, err := a.client.DeleteSecret(ctx, key, nil)
	if err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}
	return nil
}

// sanitize as Azure does not allow the '_' character in secret name
func sanitize(key string) string {
	return strings.ReplaceAll(key, "_", "-")
}

// mergeTags merges provided tags with global tags, with global tags taking precedence.
func (a *azSecretsManager) mergeTags(tags map[string]string) map[string]*string {
	merged := make(map[string]string, len(tags)+len(a.tags))

	// Add provided tags first
	maps.Copy(merged, tags)

	// Global tags override provided tags
	maps.Copy(merged, a.tags)

	// Convert to Azure tags format (map[string]*string)
	azureTags := make(map[string]*string, len(merged))
	for k, v := range merged {
		v := v
		azureTags[k] = &v
	}

	return azureTags
}
