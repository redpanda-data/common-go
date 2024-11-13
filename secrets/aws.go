package secrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type awsSecretsManager struct {
	client *secretsmanager.Client
	logger *slog.Logger
}

func NewAWSSecretsManager(ctx context.Context, logger *slog.Logger, region string, roleARN string) (SecretAPI, error) {
	cl, err := createAWSClient(ctx, region, roleARN)
	if err != nil {
		return nil, fmt.Errorf("failed to create secrets manager client: %w", err)
	}

	return &awsSecretsManager{
		client: cl,
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

func createAWSClient(ctx context.Context, region string, roleARN string) (*secretsmanager.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	if roleARN == "" {
		return secretsmanager.NewFromConfig(cfg), nil
	}

	creds := aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(sts.NewFromConfig(cfg), roleARN))
	secretsManagerClient := secretsmanager.New(secretsmanager.Options{
		Credentials: creds,
		Region:      region,
	})
	return secretsManagerClient, nil
}
