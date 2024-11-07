package secrets

import (
	"context"
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
		prefix  string
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
				prefix:  "prefix/",
			},
			wantValue:  "secretValue",
			wantExists: true,
		},
		{
			name: "should not lookup non-existing secret",
			args: args{
				secrets: map[string]string{"prefix/SECRET": "secretValue"},
				key:     "secrets.UNDEFINED",
				prefix:  "prefix/",
			},
			wantValue:  "",
			wantExists: false,
		},
		{
			name: "should not find secret with different prefix",
			args: args{
				secrets: map[string]string{"prefix/redpanda1/SECRET": "secretValue"},
				key:     "secrets.SECRET",
				prefix:  "prefix/redpanda2/",
			},
			wantValue:  "",
			wantExists: false,
		},
		{
			name: "should require variable name prefix",
			args: args{
				secrets: map[string]string{"prefix/SECRET": "secretValue"},
				key:     "SECRET",
				prefix:  "prefix/",
			},
			wantValue:  "",
			wantExists: false,
		},
		{
			name: "should extract JSON field",
			args: args{
				secrets: map[string]string{"prefix/SECRET": `{"name":"John", "age": 25, "address": {"city": "LA", "street": "Main St"}}`},
				key:     "secrets.SECRET.name",
				prefix:  "prefix/",
			},
			wantValue:  "John",
			wantExists: true,
		},
		{
			name: "should extract nested JSON field",
			args: args{
				secrets: map[string]string{"prefix/SECRET": `{"name":"John", "age": 25, "address": {"city": "LA", "street": "Main St"}}`},
				key:     "secrets.SECRET.address.city",
				prefix:  "prefix/",
			},
			wantValue:  "LA",
			wantExists: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretsApi, err := NewSecretProvider(&fakeSecretManager{
				secrets: tt.args.secrets,
			}, tt.args.prefix)
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
