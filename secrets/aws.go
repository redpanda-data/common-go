package secrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

type awsSecretsManager struct {
	client *secretsmanager.Client
	logger *slog.Logger
}

func NewAWSSecretsManager(ctx context.Context, logger *slog.Logger, region string) (SecretAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &awsSecretsManager{
		client: secretsmanager.NewFromConfig(cfg),
		logger: logger,
	}, nil
}

func (a *awsSecretsManager) GetSecretValue(ctx context.Context, key string) (string, bool) {
	value, err := a.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &key,
	})
	if err != nil {
		var nf *types.ResourceNotFoundException
		if !errors.As(err, &nf) {
			a.logger.With("error", err, "key", key).Error("Failed to look up secret")
		}
		return "", false
	}

	return *value.SecretString, true
}

func (a *awsSecretsManager) CheckSecretExists(ctx context.Context, key string) bool {
	_, err := a.client.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: &key,
	})
	return err == nil
}
