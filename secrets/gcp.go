package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type gcpSecretsManager struct {
	client    *secretmanager.Client
	projectID string
	logger    *slog.Logger
}

// NewGCPSecretsManager creates a secret API for GCP.
func NewGCPSecretsManager(ctx context.Context, logger *slog.Logger, projectID string, audience string) (SecretAPI, error) {
	var client *secretmanager.Client
	var err error

	if audience != "" {
		// Use workload identity federation
		client, err = createFederationClient(ctx, logger, audience)
		if err != nil {
			return nil, fmt.Errorf("failed to create federation client: %w", err)
		}
	} else {
		// Use default authentication
		client, err = secretmanager.NewClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create secretmanager client: %w", err)
		}
	}

	return &gcpSecretsManager{
		client:    client,
		projectID: projectID,
		logger:    logger,
	}, nil
}

func createFederationClient(ctx context.Context, logger *slog.Logger, audience string) (*secretmanager.Client, error) {
	// Service account token path
	tokenPath := "/var/run/secrets/kubernetes.io/serviceaccount/token" // #nosec G101 -- ignoring: Potential hardcoded credentials (gosec)

	// Validate token file exists and is not empty
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read service account token: %w", err)
	}
	if len(tokenBytes) == 0 {
		return nil, errors.New("service account token file is empty")
	}

	// Create credential config for federation
	credConfig := map[string]any{
		"type":               "external_account",
		"audience":           audience,
		"subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
		"credential_source": map[string]any{
			"file": tokenPath,
			"format": map[string]any{
				"type": "text",
			},
		},
	}

	// Marshal credential config to JSON bytes
	credBytes, err := json.Marshal(credConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal credential config: %w", err)
	}

	logger.Info("Creating GCP client with federation credentials", "audience", audience)

	// Create client with JSON credentials directly (no temp files)
	client, err := secretmanager.NewClient(ctx, option.WithCredentialsJSON(credBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create client with federation credentials: %w", err)
	}

	return client, nil
}

func (g *gcpSecretsManager) GetSecretValue(ctx context.Context, key string) (string, bool) {
	resp, err := g.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: g.getLatestSecretID(key),
	})
	if err != nil {
		if status.Code(err) != codes.NotFound {
			g.logger.With("error", err, "key", key).Error("Failed to look up secret")
		}
		return "", false
	}

	value := string(resp.Payload.Data)
	return value, true
}

func (g *gcpSecretsManager) CheckSecretExists(ctx context.Context, key string) bool {
	_, err := g.client.GetSecret(ctx, &secretmanagerpb.GetSecretRequest{
		Name: g.getSecretID(key),
	})
	return err == nil
}

func (g *gcpSecretsManager) CreateSecret(ctx context.Context, key string, value string) error {
	secretID := g.getSecretID(key)

	// Create the secret
	_, err := g.client.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   fmt.Sprintf("projects/%s", g.projectID),
		SecretId: key,
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_Automatic_{
					Automatic: &secretmanagerpb.Replication_Automatic{},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	// Add the secret version with the value
	_, err = g.client.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent: secretID,
		Payload: &secretmanagerpb.SecretPayload{
			Data: []byte(value),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add secret version: %w", err)
	}

	return nil
}

func (g *gcpSecretsManager) UpdateSecret(ctx context.Context, key string, value string) error {
	secretID := g.getSecretID(key)

	// Add a new secret version with the updated value
	_, err := g.client.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent: secretID,
		Payload: &secretmanagerpb.SecretPayload{
			Data: []byte(value),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}

	return nil
}

func (g *gcpSecretsManager) DeleteSecret(ctx context.Context, key string) error {
	secretID := g.getSecretID(key)

	err := g.client.DeleteSecret(ctx, &secretmanagerpb.DeleteSecretRequest{
		Name: secretID,
	})
	if err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	return nil
}

func (g *gcpSecretsManager) getLatestSecretID(key string) string {
	return fmt.Sprintf("%v/versions/latest", g.getSecretID(key))
}

func (g *gcpSecretsManager) getSecretID(key string) string {
	return fmt.Sprintf("projects/%v/secrets/%v", g.projectID, key)
}
