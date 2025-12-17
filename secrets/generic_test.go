package secrets

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSecretManager struct {
	secrets map[string]string
	tags    map[string]map[string]string // secretKey -> tags
}

func Test_secretManager_lookup(t *testing.T) {
	type args struct {
		secrets    map[string]string
		key        string
		prefix     string
		trimPrefix string
	}
	tests := []struct {
		name       string
		args       args
		wantValue  string
		wantExists bool
	}{
		{
			name: "should lookup existing secret",
			args: args{
				secrets:    map[string]string{"prefix/SECRET": "secretValue"},
				key:        "secrets.SECRET",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantValue:  "secretValue",
			wantExists: true,
		},
		{
			name: "should not lookup non-existing secret",
			args: args{
				secrets:    map[string]string{"prefix/SECRET": "secretValue"},
				key:        "secrets.UNDEFINED",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantValue:  "",
			wantExists: false,
		},
		{
			name: "should not find secret with different prefix",
			args: args{
				secrets:    map[string]string{"prefix/redpanda1/SECRET": "secretValue"},
				key:        "secrets.SECRET",
				prefix:     "prefix/redpanda2/",
				trimPrefix: "secrets.",
			},
			wantValue:  "",
			wantExists: false,
		},
		{
			name: "should require variable name prefix",
			args: args{
				secrets:    map[string]string{"prefix/SECRET": "secretValue"},
				key:        "SECRET",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantValue:  "",
			wantExists: false,
		},
		{
			name: "should extract JSON field",
			args: args{
				secrets:    map[string]string{"prefix/SECRET": `{"name":"John", "age": 25, "address": {"city": "LA", "street": "Main St"}}`},
				key:        "secrets.SECRET.name",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantValue:  "John",
			wantExists: true,
		},
		{
			name: "should extract nested JSON field",
			args: args{
				secrets:    map[string]string{"prefix/SECRET": `{"name":"John", "age": 25, "address": {"city": "LA", "street": "Main St"}}`},
				key:        "secrets.SECRET.address.city",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantValue:  "LA",
			wantExists: true,
		},
		{
			name: "should support empty trimPrefix",
			args: args{
				secrets:    map[string]string{"prefix/SECRET": "secretValue"},
				key:        "SECRET",
				prefix:     "prefix/",
				trimPrefix: "",
			},
			wantValue:  "secretValue",
			wantExists: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretsApi, err := NewSecretProvider(&fakeSecretManager{
				secrets: tt.args.secrets,
			}, tt.args.prefix, tt.args.trimPrefix)
			require.NoError(t, err)

			gotExists := secretsApi.CheckSecretExists(context.Background(), tt.args.key)
			assert.Equalf(t, tt.wantExists, gotExists, "exists(%v, %v)", context.Background(), tt.args.key)

			gotValue, gotExists := secretsApi.GetSecretValue(context.Background(), tt.args.key)
			assert.Equalf(t, tt.wantValue, gotValue, "lookup(%v, %v)", context.Background(), tt.args.key)
			assert.Equalf(t, tt.wantExists, gotExists, "lookup(%v, %v)", context.Background(), tt.args.key)
		})
	}
}

func (f *fakeSecretManager) GetSecretValue(_ context.Context, key string) (string, bool) {
	value, ok := f.secrets[key]
	return value, ok
}

func (f *fakeSecretManager) CheckSecretExists(_ context.Context, key string) bool {
	_, ok := f.secrets[key]
	return ok
}

func (f *fakeSecretManager) GetSecretLabels(_ context.Context, key string) (map[string]string, bool) {
	_, ok := f.secrets[key]
	return map[string]string{}, ok
}

func (f *fakeSecretManager) CreateSecret(_ context.Context, key string, value string, tags map[string]string) error {
	if f.secrets == nil {
		f.secrets = make(map[string]string)
	}
	if f.tags == nil {
		f.tags = make(map[string]map[string]string)
	}
	f.secrets[key] = value
	if tags != nil {
		f.tags[key] = make(map[string]string)
		for k, v := range tags {
			f.tags[key][k] = v
		}
	}
	return nil
}

func (f *fakeSecretManager) UpdateSecret(_ context.Context, key string, value string, tags map[string]string) error {
	if f.secrets == nil {
		f.secrets = make(map[string]string)
	}
	if f.tags == nil {
		f.tags = make(map[string]map[string]string)
	}
	f.secrets[key] = value
	if tags != nil {
		f.tags[key] = make(map[string]string)
		for k, v := range tags {
			f.tags[key][k] = v
		}
	}
	return nil
}

func (f *fakeSecretManager) DeleteSecret(_ context.Context, key string) error {
	if f.secrets != nil {
		delete(f.secrets, key)
	}
	return nil
}

func Test_secretManager_CreateSecret(t *testing.T) {
	type args struct {
		key        string
		value      string
		prefix     string
		trimPrefix string
	}
	tests := []struct {
		name          string
		args          args
		wantErr       bool
		wantSecretKey string
	}{
		{
			name: "should create secret with prefix",
			args: args{
				key:        "secrets.MY_SECRET",
				value:      "mySecretValue",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantErr:       false,
			wantSecretKey: "prefix/MY_SECRET",
		},
		{
			name: "should create secret without trimPrefix",
			args: args{
				key:        "MY_SECRET",
				value:      "mySecretValue",
				prefix:     "prefix/",
				trimPrefix: "",
			},
			wantErr:       false,
			wantSecretKey: "prefix/MY_SECRET",
		},
		{
			name: "should fail with invalid key format",
			args: args{
				key:        "INVALID_KEY",
				value:      "mySecretValue",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantErr:       true,
			wantSecretKey: "",
		},
		{
			name: "should create secret with JSON value",
			args: args{
				key:        "secrets.MY_SECRET",
				value:      `{"username":"admin","password":"secret123"}`,
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantErr:       false,
			wantSecretKey: "prefix/MY_SECRET",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeManager := &fakeSecretManager{
				secrets: make(map[string]string),
			}
			secretsApi, err := NewSecretProvider(fakeManager, tt.args.prefix, tt.args.trimPrefix)
			require.NoError(t, err)

			err = secretsApi.CreateSecret(context.Background(), tt.args.key, tt.args.value, nil)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Verify the secret was created with the correct key
			value, exists := fakeManager.secrets[tt.wantSecretKey]
			assert.True(t, exists, "secret should exist at key: %s", tt.wantSecretKey)
			assert.Equal(t, tt.args.value, value, "secret value should match")

			// Verify we can retrieve the secret
			retrievedValue, found := secretsApi.GetSecretValue(context.Background(), tt.args.key)
			assert.True(t, found, "should be able to retrieve the created secret")
			assert.Equal(t, tt.args.value, retrievedValue, "retrieved value should match")
		})
	}
}

func Test_secretManager_UpdateSecret(t *testing.T) {
	type args struct {
		key        string
		oldValue   string
		newValue   string
		prefix     string
		trimPrefix string
	}
	tests := []struct {
		name          string
		args          args
		wantErr       bool
		wantSecretKey string
	}{
		{
			name: "should update existing secret",
			args: args{
				key:        "secrets.MY_SECRET",
				oldValue:   "oldValue",
				newValue:   "newValue",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantErr:       false,
			wantSecretKey: "prefix/MY_SECRET",
		},
		{
			name: "should fail with invalid key format",
			args: args{
				key:        "INVALID_KEY",
				oldValue:   "oldValue",
				newValue:   "newValue",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantErr:       true,
			wantSecretKey: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeManager := &fakeSecretManager{
				secrets: make(map[string]string),
			}
			// Pre-create the secret with old value
			if tt.wantSecretKey != "" {
				fakeManager.secrets[tt.wantSecretKey] = tt.args.oldValue
			}

			secretsApi, err := NewSecretProvider(fakeManager, tt.args.prefix, tt.args.trimPrefix)
			require.NoError(t, err)

			err = secretsApi.UpdateSecret(context.Background(), tt.args.key, tt.args.newValue, nil)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Verify the secret was updated
			value, exists := fakeManager.secrets[tt.wantSecretKey]
			assert.True(t, exists, "secret should exist at key: %s", tt.wantSecretKey)
			assert.Equal(t, tt.args.newValue, value, "secret value should be updated")

			// Verify we can retrieve the updated secret
			retrievedValue, found := secretsApi.GetSecretValue(context.Background(), tt.args.key)
			assert.True(t, found, "should be able to retrieve the updated secret")
			assert.Equal(t, tt.args.newValue, retrievedValue, "retrieved value should match new value")
		})
	}
}

func Test_secretManager_DeleteSecret(t *testing.T) {
	type args struct {
		key        string
		value      string
		prefix     string
		trimPrefix string
	}
	tests := []struct {
		name          string
		args          args
		wantErr       bool
		wantSecretKey string
	}{
		{
			name: "should delete existing secret",
			args: args{
				key:        "secrets.MY_SECRET",
				value:      "secretValue",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantErr:       false,
			wantSecretKey: "prefix/MY_SECRET",
		},
		{
			name: "should fail with invalid key format",
			args: args{
				key:        "INVALID_KEY",
				value:      "secretValue",
				prefix:     "prefix/",
				trimPrefix: "secrets.",
			},
			wantErr:       true,
			wantSecretKey: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeManager := &fakeSecretManager{
				secrets: make(map[string]string),
			}
			// Pre-create the secret
			if tt.wantSecretKey != "" {
				fakeManager.secrets[tt.wantSecretKey] = tt.args.value
			}

			secretsApi, err := NewSecretProvider(fakeManager, tt.args.prefix, tt.args.trimPrefix)
			require.NoError(t, err)

			// Verify secret exists before deletion
			if !tt.wantErr {
				exists := secretsApi.CheckSecretExists(context.Background(), tt.args.key)
				assert.True(t, exists, "secret should exist before deletion")
			}

			err = secretsApi.DeleteSecret(context.Background(), tt.args.key)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Verify the secret was deleted
			_, exists := fakeManager.secrets[tt.wantSecretKey]
			assert.False(t, exists, "secret should not exist after deletion")

			// Verify we cannot retrieve the deleted secret
			_, found := secretsApi.GetSecretValue(context.Background(), tt.args.key)
			assert.False(t, found, "should not be able to retrieve deleted secret")
		})
	}
}

type fakeSecretManagerWithGlobalTags struct {
	secrets    map[string]string
	tags       map[string]map[string]string
	globalTags map[string]string
}

func (f *fakeSecretManagerWithGlobalTags) GetSecretValue(_ context.Context, key string) (string, bool) {
	value, ok := f.secrets[key]
	return value, ok
}

func (f *fakeSecretManagerWithGlobalTags) CheckSecretExists(_ context.Context, key string) bool {
	_, ok := f.secrets[key]
	return ok
}

func (f *fakeSecretManagerWithGlobalTags) GetSecretLabels(_ context.Context, key string) (map[string]string, bool) {
	_, ok := f.secrets[key]
	return map[string]string{}, ok
}

func (f *fakeSecretManagerWithGlobalTags) CreateSecret(_ context.Context, key string, value string, tags map[string]string) error {
	if f.secrets == nil {
		f.secrets = make(map[string]string)
	}
	if f.tags == nil {
		f.tags = make(map[string]map[string]string)
	}
	f.secrets[key] = value

	// Merge tags: provided tags first, then global tags override
	merged := make(map[string]string, len(tags)+len(f.globalTags))
	maps.Copy(merged, tags)
	maps.Copy(merged, f.globalTags)
	f.tags[key] = merged
	return nil
}

func (f *fakeSecretManagerWithGlobalTags) UpdateSecret(_ context.Context, key string, value string, tags map[string]string) error {
	if f.secrets == nil {
		f.secrets = make(map[string]string)
	}
	if f.tags == nil {
		f.tags = make(map[string]map[string]string)
	}
	f.secrets[key] = value

	// Merge tags: provided tags first, then global tags override
	merged := make(map[string]string, len(tags)+len(f.globalTags))
	maps.Copy(merged, tags)
	maps.Copy(merged, f.globalTags)
	f.tags[key] = merged
	return nil
}

func (f *fakeSecretManagerWithGlobalTags) DeleteSecret(_ context.Context, key string) error {
	if f.secrets != nil {
		delete(f.secrets, key)
	}
	if f.tags != nil {
		delete(f.tags, key)
	}
	return nil
}

func Test_secretManager_TagOverwriting(t *testing.T) {
	tests := []struct {
		name         string
		globalTags   map[string]string
		providedTags map[string]string
		expectedTags map[string]string
	}{
		{
			name: "global tags should overwrite provided tags with same keys",
			globalTags: map[string]string{
				"env":  "dev",
				"team": "platform",
			},
			providedTags: map[string]string{
				"env": "prod",
			},
			expectedTags: map[string]string{
				"env":  "dev",
				"team": "platform",
			},
		},
		{
			name: "global tags and provided tags should be merged when no conflict",
			globalTags: map[string]string{
				"env":  "dev",
				"team": "platform",
			},
			providedTags: map[string]string{
				"owner": "john",
			},
			expectedTags: map[string]string{
				"env":   "dev",
				"team":  "platform",
				"owner": "john",
			},
		},
		{
			name:       "provided tags work when no global tags",
			globalTags: map[string]string{},
			providedTags: map[string]string{
				"env": "staging",
			},
			expectedTags: map[string]string{
				"env": "staging",
			},
		},
		{
			name: "all global tags overwrite all provided tags",
			globalTags: map[string]string{
				"env":  "dev",
				"team": "platform",
			},
			providedTags: map[string]string{
				"env":  "prod",
				"team": "data",
			},
			expectedTags: map[string]string{
				"env":  "dev",
				"team": "platform",
			},
		},
		{
			name: "global tags used when no provided tags",
			globalTags: map[string]string{
				"env":  "dev",
				"team": "platform",
			},
			providedTags: nil,
			expectedTags: map[string]string{
				"env":  "dev",
				"team": "platform",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeManager := &fakeSecretManagerWithGlobalTags{
				secrets:    make(map[string]string),
				tags:       make(map[string]map[string]string),
				globalTags: tt.globalTags,
			}

			secretsApi, err := NewSecretProvider(fakeManager, "prefix/", "secrets.")
			require.NoError(t, err)

			// Test CreateSecret
			err = secretsApi.CreateSecret(context.Background(), "secrets.MY_SECRET", "myValue", tt.providedTags)
			require.NoError(t, err)

			actualTags := fakeManager.tags["prefix/MY_SECRET"]
			assert.Equal(t, tt.expectedTags, actualTags, "CreateSecret: tags should match expected")

			// Test UpdateSecret
			err = secretsApi.UpdateSecret(context.Background(), "secrets.MY_SECRET", "newValue", tt.providedTags)
			require.NoError(t, err)

			actualTags = fakeManager.tags["prefix/MY_SECRET"]
			assert.Equal(t, tt.expectedTags, actualTags, "UpdateSecret: tags should match expected")
		})
	}
}
