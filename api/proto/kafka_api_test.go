package proto

import (
	"testing"

	commonv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1"
	"github.com/stretchr/testify/require"
)

func TestIntToKafkaAPIKey(t *testing.T) {
	// Generate a mapping of the first 100 keys and make sure we
	// get the same number of unique keys as are in the proto
	mapped := map[commonv1.KafkaAPIKey]struct{}{}
	for i := range 100 {
		mapped[IntToKafkaAPIKey(i)] = struct{}{}
	}

	require.Equal(t, len(mapped), len(commonv1.KafkaAPIKey_name))
}
