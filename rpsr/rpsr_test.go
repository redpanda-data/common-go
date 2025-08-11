package rpsr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_aclKey_Unique(t *testing.T) {
	acl1 := ACL{Principal: "fo", Resource: "obar"}
	acl2 := ACL{Principal: "foo", ResourceType: "bar"}
	require.NotEqual(t, aclKey(acl1), aclKey(acl2))
}

func TestParseOperation(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected Operation
		wantErr  bool
	}{
		{
			name:     "all operation",
			input:    "all",
			expected: OperationAll,
			wantErr:  false,
		},
		{
			name:     "read operation",
			input:    "read",
			expected: OperationRead,
			wantErr:  false,
		},
		{
			name:     "write operation",
			input:    "write",
			expected: OperationWrite,
			wantErr:  false,
		},
		{
			name:     "delete operation",
			input:    "delete",
			expected: OperationDelete,
			wantErr:  false,
		},
		{
			name:     "describe operation",
			input:    "describe",
			expected: OperationDescribe,
			wantErr:  false,
		},
		{
			name:     "describe_configs with underscore (gets normalized)",
			input:    "describe_configs",
			expected: OperationDescribeConfig,
			wantErr:  false,
		},
		{
			name:     "describeconfigs normalized form",
			input:    "describeconfigs",
			expected: OperationDescribeConfig,
			wantErr:  false,
		},
		{
			name:     "alter operation",
			input:    "alter",
			expected: OperationAlter,
			wantErr:  false,
		},
		{
			name:     "alter_configs with underscore (gets normalized)",
			input:    "alter_configs",
			expected: OperationAlterConfig,
			wantErr:  false,
		},
		{
			name:     "alterconfigs normalized form",
			input:    "alterconfigs",
			expected: OperationAlterConfig,
			wantErr:  false,
		},
		{
			name:     "case insensitive operation",
			input:    "READ",
			expected: OperationRead,
			wantErr:  false,
		},
		{
			name:     "operation with spaces",
			input:    " write ",
			expected: OperationWrite,
			wantErr:  false,
		},
		{
			name:     "unknown operation",
			input:    "unknown",
			expected: "",
			wantErr:  true,
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseOperation(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}
