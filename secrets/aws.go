package secrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"

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
	tags   map[string]string
}

// NewAWSSecretsManager creates a secret API for AWS.
// The optional globalTags parameter specifies tags that will be applied to all secrets.
func NewAWSSecretsManager(ctx context.Context, logger *slog.Logger, region string, roleARN string, globalTags ...map[string]string) (SecretAPI, error) {
	cl, err := createAWSClient(ctx, region, roleARN)
	if err != nil {
		return nil, fmt.Errorf("failed to create secrets manager client: %w", err)
	}

	tags := make(map[string]string)
	if len(globalTags) > 0 && globalTags[0] != nil {
		for k, v := range globalTags[0] {
			tags[k] = v
		}
	}

	return &awsSecretsManager{
		client: cl,
		logger: logger,
		tags:   tags,
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

func (a *awsSecretsManager) GetSecretLabels(ctx context.Context, key string) (map[string]string, bool) {
	secret, err := a.client.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: &key,
	})
	if err != nil {
		return nil, false
	}
	return convert(secret), true
}

func convert(secret *secretsmanager.DescribeSecretOutput) map[string]string {
	labels := make(map[string]string)
	for _, tag := range secret.Tags {
		if tag.Key != nil && tag.Value != nil {
			labels[*tag.Key] = *tag.Value
		}
	}
	return labels
}

func (a *awsSecretsManager) CreateSecret(ctx context.Context, key string, value string, tags map[string]string) error {
	mergedTags := a.mergeTags(tags)

	_, err := a.client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         &key,
		SecretString: &value,
		Tags:         mergedTags,
	})
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}
	return nil
}

func (a *awsSecretsManager) UpdateSecret(ctx context.Context, key string, value string, tags map[string]string) error {
	// Update the secret value
	_, err := a.client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     &key,
		SecretString: &value,
	})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}

	// Get current secret to determine which tags to remove
	describeResp, err := a.client.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: &key,
	})
	if err != nil {
		return fmt.Errorf("failed to describe secret: %w", err)
	}

	// Merge tags with global tags
	mergedTags := a.mergeTags(tags)

	// Determine tags to remove (existing tags not in merged tags)
	existingTags := make(map[string]string)
	for _, tag := range describeResp.Tags {
		if tag.Key != nil && tag.Value != nil {
			existingTags[*tag.Key] = *tag.Value
		}
	}

	keysToRemove := make([]string, 0)
	for k := range existingTags {
		found := false
		for _, tag := range mergedTags {
			if tag.Key != nil && *tag.Key == k {
				found = true
				break
			}
		}
		if !found {
			keysToRemove = append(keysToRemove, k)
		}
	}

	// Remove old tags
	if len(keysToRemove) > 0 {
		_, err = a.client.UntagResource(ctx, &secretsmanager.UntagResourceInput{
			SecretId: &key,
			TagKeys:  keysToRemove,
		})
		if err != nil {
			return fmt.Errorf("failed to untag secret: %w", err)
		}
	}

	// Add/update tags
	if len(mergedTags) > 0 {
		_, err = a.client.TagResource(ctx, &secretsmanager.TagResourceInput{
			SecretId: &key,
			Tags:     mergedTags,
		})
		if err != nil {
			return fmt.Errorf("failed to tag secret: %w", err)
		}
	}

	return nil
}

func (a *awsSecretsManager) DeleteSecret(ctx context.Context, key string) error {
	forceDelete := true
	_, err := a.client.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
		SecretId:                   &key,
		ForceDeleteWithoutRecovery: &forceDelete,
	})
	if err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}
	return nil
}

// mergeTags merges provided tags with global tags, with global tags taking precedence.
func (a *awsSecretsManager) mergeTags(tags map[string]string) []types.Tag {
	merged := make(map[string]string, len(tags)+len(a.tags))

	// Add provided tags first
	maps.Copy(merged, tags)

	// Global tags override provided tags
	maps.Copy(merged, a.tags)

	// Convert to AWS Tag slice
	awsTags := make([]types.Tag, 0, len(merged))
	for k, v := range merged {
		k, v := k, v
		awsTags = append(awsTags, types.Tag{
			Key:   &k,
			Value: &v,
		})
	}

	return awsTags
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
