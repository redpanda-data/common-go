package rpadmin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDebugBundleOption(t *testing.T) {
	opts := []DebugBundleOption{
		WithSCRAMAuthentication("user0", "pass0", ScramSha512),
		WithCPUProfilerWaitSeconds(10),
		WithControllerLogsSizeLimitBytes(20),
		WithLogsSizeLimitBytes(30),
		WithMetricsIntervalSeconds(40),
		WithLogsSince("yesterday"),
		WithLogsUntil("2025-10-20"),
		WithPartitions([]string{"1", "2", "3"}),
	}

	params := &debugBundleStartConfigParameters{}
	for _, o := range opts {
		o.apply(params)
	}

	authScram, ok := params.Authentication.(debugBundleSCRAMAuthentication)
	assert.True(t, ok)
	assert.Equal(t, "user0", authScram.Username)
	assert.Equal(t, "pass0", authScram.Password)
	assert.Equal(t, ScramSha512, authScram.Mechanism)
	assert.Equal(t, int32(10), params.CPUProfilerWaitSeconds)
	assert.Equal(t, int32(20), params.ControllerLogsSizeLimitBytes)
	assert.Equal(t, int32(30), params.LogsSizeLimitBytes)
	assert.Equal(t, int32(40), params.MetricsIntervalSeconds)
	assert.Equal(t, "yesterday", params.LogsSince)
	assert.Equal(t, "2025-10-20", params.LogsUntil)
	assert.Equal(t, []string{"1", "2", "3"}, params.Partitions)

	pj, _ := json.Marshal(params)
	assert.Equal(t, `{"authentication":{"mechanism":"SCRAM-SHA-512","username":"user0","password":"pass0"},"controller_logs_size_limit_bytes":20,"logs_size_limit_bytes":30,"cpu_profiler_wait_seconds":10,"metrics_interval_seconds":40,"logs_since":"yesterday","logs_until":"2025-10-20","partition":["1","2","3"]}`, string(pj))

	opts = []DebugBundleOption{
		WithOIDCAuthentication("token0"),
	}
	params = &debugBundleStartConfigParameters{}
	for _, o := range opts {
		o.apply(params)
	}

	authOIDC, ok := params.Authentication.(debugBundleOIDCAuthentication)
	assert.True(t, ok)
	assert.Equal(t, "token0", authOIDC.Token)
	assert.Equal(t, int32(0), params.CPUProfilerWaitSeconds)
	assert.Equal(t, int32(0), params.ControllerLogsSizeLimitBytes)
	assert.Equal(t, int32(0), params.LogsSizeLimitBytes)
	assert.Equal(t, int32(0), params.MetricsIntervalSeconds)
	assert.Equal(t, "", params.LogsSince)
	assert.Equal(t, "", params.LogsUntil)
	assert.Empty(t, params.Partitions)

	pj, _ = json.Marshal(params)
	assert.Equal(t, `{"authentication":{"token":"token0"}}`, string(pj))
}
