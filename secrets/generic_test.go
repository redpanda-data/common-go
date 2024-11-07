package secrets

import (
	"context"
	"log/slog"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSecretManager struct {
	secrets map[string]string
}

func Test_secretManager_lookup(t *testing.T) {
	type args struct {
		secrets map[string]string
		key     string
		url     string
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
				secrets: map[string]string{"prefix/SECRET": "secretValue"},
				key:     "secrets.SECRET",
				url:     "aws://eu-west-1/prefix/",
			},
			wantValue:  "secretValue",
			wantExists: true,
		},
		{
			name: "should not lookup non-existing secret",
			args: args{
				secrets: map[string]string{"prefix/SECRET": "secretValue"},
				key:     "secrets.UNDEFINED",
				url:     "aws://eu-west-1/prefix/",
			},
			wantValue:  "",
			wantExists: false,
		},
		{
			name: "should not find secret with different prefix",
			args: args{
				secrets: map[string]string{"prefix/redpanda1/SECRET": "secretValue"},
				key:     "secrets.SECRET",
				url:     "aws://eu-west-1/prefix/redpanda2/",
			},
			wantValue:  "",
			wantExists: false,
		},
		{
			name: "should require variable name prefix",
			args: args{
				secrets: map[string]string{"prefix/SECRET": "secretValue"},
				key:     "SECRET",
				url:     "aws://eu-west-1/prefix/",
			},
			wantValue:  "",
			wantExists: false,
		},
		{
			name: "should extract JSON field",
			args: args{
				secrets: map[string]string{"prefix/SECRET": `{"name":"John", "age": 25, "address": {"city": "LA", "street": "Main St"}}`},
				key:     "secrets.SECRET.name",
				url:     "aws://eu-west-1/prefix/",
			},
			wantValue:  "John",
			wantExists: true,
		},
		{
			name: "should extract nested JSON field",
			args: args{
				secrets: map[string]string{"prefix/SECRET": `{"name":"John", "age": 25, "address": {"city": "LA", "street": "Main St"}}`},
				key:     "secrets.SECRET.address.city",
				url:     "aws://eu-west-1/prefix/",
			},
			wantValue:  "LA",
			wantExists: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedURL, err := url.Parse(tt.args.url)
			require.NoError(t, err)
			secretsApi, err := NewSecretProvider(context.Background(), slog.Default(), parsedURL, func(ctx context.Context, logger *slog.Logger, url *url.URL) (SecretAPI, error) {
				return &fakeSecretManager{
					secrets: tt.args.secrets,
				}, nil
			})
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
