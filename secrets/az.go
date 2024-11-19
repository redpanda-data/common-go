package secrets

import (
	"context"
	"fmt"
	"log/slog"
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
}

func NewAzSecretsManager(logger *slog.Logger, vaultURL string) (SecretAPI, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	client, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create secretmanager client: %w", err)
	}

	return &azSecretsManager{
		client: client,
		logger: logger,
	}, nil
}

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

func (a *azSecretsManager) CheckSecretExists(ctx context.Context, key string) bool {
	key = sanitize(key)
	pager := a.client.NewListSecretVersionsPager(key, nil)
	if !pager.More() {
		return false
	}

	page, err := pager.NextPage(ctx)
	return err == nil && len(page.Value) > 0
}

// sanitize as Azure does not allow the '_' character in secret name
func sanitize(key string) string {
	return strings.ReplaceAll(key, "_", "-")
}
