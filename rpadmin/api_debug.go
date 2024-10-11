// Copyright 2023 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package rpadmin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const (
	debugEndpoint       = "/v1/debug"
	selfTestEndpoint    = debugEndpoint + "/self_test"
	cpuProfilerEndpoint = debugEndpoint + "/cpu_profile"
	bundleEndpoint      = debugEndpoint + "/bundle"

	// DiskcheckTagIdentifier is the type identifier for disk self tests.
	DiskcheckTagIdentifier = "disk"
	// NetcheckTagIdentifier is the type identifier for network self tests.
	NetcheckTagIdentifier = "network"
	// CloudcheckTagIdentifier is the type identifier for cloud self tests.
	CloudcheckTagIdentifier = "cloud"

	// DebugBundleErrorCodeOk no error.
	DebugBundleErrorCodeOk = 0
	// DebugBundleErrorCodeProcessAlreadyRunning debug process is already running.
	DebugBundleErrorCodeProcessAlreadyRunning = 1
	// DebugBundleErrorCodeProcessNotRunning debug process not already running.
	DebugBundleErrorCodeProcessNotRunning = 2
	// DebugBundleErrorCodeInvalidJobID provided job id is invalid.
	DebugBundleErrorCodeInvalidJobID = 3
	// DebugBundleErrorCodeProcessNotStarted process was not started.
	DebugBundleErrorCodeProcessNotStarted = 4
	// DebugBundleErrorCodeInsufficientResources there are insufficient resources to start debug bundle process.
	DebugBundleErrorCodeInsufficientResources = 5
	// DebugBundleErrorCodeInternalError other internal unknown error.
	DebugBundleErrorCodeInternalError = 6
)

// A SelfTestNodeResult describes the results of a particular self-test run.
// Currently there are only two types of tests, disk & network. The struct
// contains latency and throughput metrics as well as any errors or warnings
// that may have arisen during the tests execution.
type SelfTestNodeResult struct {
	P50            *uint   `json:"p50,omitempty"`
	P90            *uint   `json:"p90,omitempty"`
	P99            *uint   `json:"p99,omitempty"`
	P999           *uint   `json:"p999,omitempty"`
	MaxLatency     *uint   `json:"max_latency,omitempty"`
	RequestsPerSec *uint   `json:"rps,omitempty"`
	BytesPerSec    *uint   `json:"bps,omitempty"`
	Timeouts       uint    `json:"timeouts"`
	TestID         string  `json:"test_id"`
	TestName       string  `json:"name"`
	TestInfo       string  `json:"info"`
	TestType       string  `json:"test_type"`
	StartTime      int64   `json:"start_time"`
	EndTime        int64   `json:"end_time"`
	Duration       uint    `json:"duration"`
	Warning        *string `json:"warning,omitempty"`
	Error          *string `json:"error,omitempty"`
}

// SelfTestNodeReport describes the result returned from one member of the cluster.
// A query for results will return an array of these structs, one from each member.
type SelfTestNodeReport struct {
	// NodeID is the node ID.
	NodeID int `json:"node_id"`
	// One of { "idle", "running", "unreachable" }
	//
	// If a status of idle is returned, the following `Results` variable will not
	// be nil. In all other cases it will be nil.
	Status string `json:"status"`
	// One of { "idle", "net", "disk", "cloud" }
	Stage string `json:"stage"`
	// If value of `Status` is "idle", then this field will contain one result for
	// each peer involved in the test. It represents the results of the last
	// successful test run.
	Results []SelfTestNodeResult `json:"results,omitempty"`
}

// DiskcheckParameters describes what parameters redpanda will use when starting the diskcheck benchmark.
type DiskcheckParameters struct {
	// Name is the descriptive name given to test run
	Name string `json:"name"`
	// Open the file with O_DSYNC flag option
	DSync bool `json:"dsync"`
	// Set to true to disable the write portion of the benchmark
	SkipWrite bool `json:"skip_write"`
	// Set to true to disable the read portion of the benchmark
	SkipRead bool `json:"skip_read"`
	// Total size of all benchmark files to exist on disk
	DataSize uint `json:"data_size"`
	// Size of individual read and/or write requests
	RequestSize uint `json:"request_size"`
	// Total duration of the benchmark
	DurationMs uint `json:"duration_ms"`
	// Amount of fibers to run per shard
	Parallelism uint `json:"parallelism"`
	// Filled in automatically by the \ref StartSelfTest method
	Type string `json:"type"`
}

// NetcheckParameters describes what parameters redpanda will use when starting the netcheck benchmark.
type NetcheckParameters struct {
	// Name is descriptive name given to test run
	Name string `json:"name"`
	// Size of individual request
	RequestSize uint `json:"request_size"`
	// Total duration of an individual benchmark
	DurationMs uint `json:"duration_ms"`
	// Number of fibers per shard used to make network requests
	Parallelism uint `json:"parallelism"`
	// Filled in automatically by the \ref StartSelfTest method
	Type string `json:"type"`
}

// CloudcheckParameters describes what parameters redpanda will use when starting the netcheck benchmark.
type CloudcheckParameters struct {
	// Descriptive name given to test run
	Name string `json:"name"`
	// Timeout duration of a network request
	TimeoutMs uint `json:"timeout_ms"`
	// Backoff duration of a network request
	BackoffMs uint `json:"backoff_ms"`
	// Filled in automatically by the \ref StartSelfTest method
	Type string `json:"type"`
}

// SelfTestRequest represents the body of a self-test start POST request.
type SelfTestRequest struct {
	Tests []any `json:"tests,omitempty"`
	Nodes []int `json:"nodes,omitempty"`
}

// PartitionLeaderTable is the information about leaders, the information comes
// from the Redpanda's partition_leaders_table.
type PartitionLeaderTable struct {
	Ns                   string `json:"ns"`
	Topic                string `json:"topic"`
	PartitionID          int    `json:"partition_id"`
	Leader               int    `json:"leader"`
	PreviousLeader       int    `json:"previous_leader"`
	LastStableLeaderTerm int    `json:"last_stable_leader_term"`
	UpdateTerm           int    `json:"update_term"`
	PartitionRevision    int    `json:"partition_revision"`
}

// ControllerStatus is the status of a controller, as seen by a node.
type ControllerStatus struct {
	StartOffset       int `json:"start_offset"`
	LastAppliedOffset int `json:"last_applied_offset"`
	CommittedIndex    int `json:"committed_index"`
	DirtyOffset       int `json:"dirty_offset"`
}

// ReplicaState is the partition state of a replica. There are many keys
// returned, so the raw response is just unmarshalled into an interface.
type ReplicaState map[string]any

// DebugPartition is the low level debug information of a partition.
type DebugPartition struct {
	Ntp      string         `json:"ntp"`
	Replicas []ReplicaState `json:"replicas"`
}

// debugBundleStartConfigParameters are the configuration parameters to starting a
// debug bundle process.
// See rpk debug bundle --help
type debugBundleStartConfigParameters struct {
	// one of DebugBundleSCRAMAuthentication or DebugBundleOIDCAuthentication
	Authentication               any                        `json:"authentication,omitempty"`
	ControllerLogsSizeLimitBytes int32                      `json:"controller_logs_size_limit_bytes,omitempty"`
	LogsSizeLimitBytes           int32                      `json:"logs_size_limit_bytes,omitempty"`
	CPUProfilerWaitSeconds       int32                      `json:"cpu_profiler_wait_seconds,omitempty"`
	MetricsIntervalSeconds       int32                      `json:"metrics_interval_seconds,omitempty"`
	MetricsSamples               int32                      `json:"metrics_samples,omitempty"`
	LogsSince                    string                     `json:"logs_since,omitempty"`
	LogsUntil                    string                     `json:"logs_until,omitempty"`
	Partitions                   []string                   `json:"partition,omitempty"`
	TLSEnabled                   bool                       `json:"tls_enabled,omitempty"`
	TLSSkipInsecureVerify        bool                       `json:"tls_insecure_skip_verify,omitempty"`
	Namespace                    string                     `json:"namespace,omitempty"`
	LabelSelector                []DebugBundleLabelSelector `json:"label_selector,omitempty"`
}

// DebugBundleLabelSelector is the label selector parameters
type DebugBundleLabelSelector struct {
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}

// debugBundleSCRAMAuthentication are the SCRAM authentication parameters.
type debugBundleSCRAMAuthentication struct {
	Mechanism string `json:"mechanism,omitempty"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
}

type debugBundleStartConfig struct {
	JobID  string                           `json:"job_id,omitempty"`
	Config debugBundleStartConfigParameters `json:"config,omitempty"`
}

// DebugBundleOption are options for setting debug bundle parameters.
type DebugBundleOption interface {
	apply(*debugBundleStartConfigParameters)
}

type debugBundleOpt struct {
	fn func(*debugBundleStartConfigParameters)
}

func (opt debugBundleOpt) apply(cfg *debugBundleStartConfigParameters) { opt.fn(cfg) }

// WithSCRAMAuthentication sets SCRAM authentication.
func WithSCRAMAuthentication(username, password, mechanism string) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.Authentication = debugBundleSCRAMAuthentication{
			Username: username, Password: password, Mechanism: mechanism,
		}
	}}
}

// WithControllerLogsSizeLimitBytes sets the controller-logs-size-limit parameter.
func WithControllerLogsSizeLimitBytes(v int32) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.ControllerLogsSizeLimitBytes = v
	}}
}

// WithLogsSizeLimitBytes sets the logs-size-limit parameter.
func WithLogsSizeLimitBytes(v int32) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.LogsSizeLimitBytes = v
	}}
}

// WithCPUProfilerWaitSeconds sets the cpu-profiler-wait parameter.
func WithCPUProfilerWaitSeconds(v int32) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.CPUProfilerWaitSeconds = v
	}}
}

// WithMetricsIntervalSeconds sets the metrics-interval parameter.
func WithMetricsIntervalSeconds(v int32) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.MetricsIntervalSeconds = v
	}}
}

// WithMetricsSamples sets the metrics samples parameter.
func WithMetricsSamples(v int32) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.MetricsSamples = v
	}}
}

// WithLogsSince sets the logs-since parameter.
func WithLogsSince(v string) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.LogsSince = v
	}}
}

// WithLogsUntil sets the logs-until parameter.
func WithLogsUntil(v string) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.LogsUntil = v
	}}
}

// WithPartitions sets the partitions parameter.
func WithPartitions(v []string) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.Partitions = v
	}}
}

// WithTLS sets the TLS settings into the config.
func WithTLS(enabled bool, insecureSkipVerify bool) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.TLSEnabled = enabled
		param.TLSSkipInsecureVerify = insecureSkipVerify
	}}
}

// WithNamespace sets the Kubernetes namespace settings into the config.
func WithNamespace(ns string) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.Namespace = ns
	}}
}

// WithLabelSelector sets SCRAM authentication.
func WithLabelSelector(list []DebugBundleLabelSelector) DebugBundleOption {
	return debugBundleOpt{func(param *debugBundleStartConfigParameters) {
		param.LabelSelector = list
	}}
}

// DebugBundleStartResponse is the response to debug bundle api.
type DebugBundleStartResponse struct {
	JobID string `json:"job_id,omitempty"`
}

// DebugBundleStatus contains information about
type DebugBundleStatus struct {
	JobID string `json:"job_id,omitempty"`
	// one of RUNNING|SUCCESS|ERROR|EXPIRED
	Status string `json:"status,omitempty"`
	// When the job was started, in milliseconds since epoch
	Created  int64  `json:"created,omitempty"`
	Size     int64  `json:"file_size,omitempty"`
	Filename string `json:"filename,omitempty"`
	// Only filled in once the process completes.  Content of stdout from rpk.
	Stdout []string `json:"stdout,omitempty"`
	// Only filled in once the process completes.  Content of stderr from rpk.
	Stderr []string `json:"stderr,omitempty"`
}

// StartSelfTest starts the self test.
func (a *AdminAPI) StartSelfTest(ctx context.Context, nodeIds []int, params []any) (string, error) {
	var testID string
	body := SelfTestRequest{
		Tests: params,
		Nodes: nodeIds,
	}
	err := a.sendToLeader(ctx,
		http.MethodPost,
		fmt.Sprintf("%s/start", selfTestEndpoint),
		body,
		&testID)
	return testID, err
}

// StopSelfTest stops the self test.
func (a *AdminAPI) StopSelfTest(ctx context.Context) error {
	return a.sendToLeader(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/stop", selfTestEndpoint),
		nil,
		nil,
	)
}

// SelfTestStatus gets the self test status.
func (a *AdminAPI) SelfTestStatus(ctx context.Context) ([]SelfTestNodeReport, error) {
	var response []SelfTestNodeReport
	err := a.sendAny(ctx, http.MethodGet, fmt.Sprintf("%s/status", selfTestEndpoint), nil, &response)
	return response, err
}

// PartitionLeaderTable returns the partitions leader table for the requested
// node.
func (a *AdminAPI) PartitionLeaderTable(ctx context.Context) ([]PartitionLeaderTable, error) {
	var response []PartitionLeaderTable
	return response, a.sendAny(ctx, http.MethodGet, "/v1/debug/partition_leaders_table", nil, &response)
}

// IsNodeIsolated gets the is node isolated status.
func (a *AdminAPI) IsNodeIsolated(ctx context.Context) (bool, error) {
	var isIsolated bool
	return isIsolated, a.sendAny(ctx, http.MethodGet, "/v1/debug/is_node_isolated", nil, &isIsolated)
}

// ControllerStatus returns the controller status, as seen by the requested
// node.
func (a *AdminAPI) ControllerStatus(ctx context.Context) (ControllerStatus, error) {
	var response ControllerStatus
	return response, a.sendAny(ctx, http.MethodGet, "/v1/debug/controller_status", nil, &response)
}

// DebugPartition returns low level debug information (on any node) of all
// replicas of a given partition.
func (a *AdminAPI) DebugPartition(ctx context.Context, namespace, topic string, partitionID int) (DebugPartition, error) {
	var response DebugPartition
	return response, a.sendAny(ctx, http.MethodGet, fmt.Sprintf("/v1/debug/partition/%v/%v/%v", namespace, topic, partitionID), nil, &response)
}

// RawCPUProfile returns the raw response of the CPU profiler.
func (a *AdminAPI) RawCPUProfile(ctx context.Context, wait time.Duration) ([]byte, error) {
	var response []byte
	path := fmt.Sprintf("%v?wait_ms=%v", cpuProfilerEndpoint, strconv.Itoa(int(wait.Milliseconds())))
	return response, a.sendAny(ctx, http.MethodGet, path, nil, &response)
}

// RestartService restarts a Redpanda service, either http-proxy or schema-registry.
func (a *AdminAPI) RestartService(ctx context.Context, service string) error {
	return a.sendAny(ctx, http.MethodPut, fmt.Sprintf("/v1/debug/restart_service?service=%s", service), nil, nil)
}

// CreateDebugBundle starts the debug bundle process.
// This should be called using Host client to issue a request against a specific broker node.
// jobID is the user specified job UUID.
func (a *AdminAPI) CreateDebugBundle(ctx context.Context, jobID string, opts ...DebugBundleOption) (DebugBundleStartResponse, error) {
	config := &debugBundleStartConfigParameters{}
	for _, o := range opts {
		o.apply(config)
	}
	body := debugBundleStartConfig{
		JobID:  jobID,
		Config: *config,
	}
	var response DebugBundleStartResponse

	err := a.sendOne(ctx, http.MethodPost, bundleEndpoint, body, &response, false)
	return response, err
}

// GetDebugBundleStatus gets the current debug bundle process status on the specified broker node.
// This should be called using Host client to issue a request against a specific broker node.
func (a *AdminAPI) GetDebugBundleStatus(ctx context.Context) (DebugBundleStatus, error) {
	var response DebugBundleStatus
	err := a.sendOne(ctx, http.MethodGet, bundleEndpoint, nil, &response, false)
	return response, err
}

// CancelDebugBundleProcess cancels the specific debug bundle process that's running.
// This should be called using Host client to issue a request against a specific broker node.
func (a *AdminAPI) CancelDebugBundleProcess(ctx context.Context, jobID string) error {
	err := a.sendOne(ctx, http.MethodDelete, fmt.Sprintf("%s/%s", bundleEndpoint, jobID), nil, nil, false)
	return err
}

// DeleteDebugBundleFile deletes the specific debug bundle file on the specified broker node.
// This should be called using Host client to issue a request against a specific broker node.
func (a *AdminAPI) DeleteDebugBundleFile(ctx context.Context, filename string) error {
	return a.sendOne(ctx, http.MethodDelete, fmt.Sprintf("%s/file/%s", bundleEndpoint, filename), nil, nil, false)
}

// DownloadDebugBundleFile gets the specific debug bundle file on the specified broker node.
func (a *AdminAPI) DownloadDebugBundleFile(ctx context.Context, filename string) (*http.Response, error) {
	if len(a.urls) != 1 {
		return nil, fmt.Errorf("unable to issue a single-admin-endpoint request to %d admin endpoints", len(a.urls))
	}
	url := a.urls[0] + fmt.Sprintf("%s/file/%s", bundleEndpoint, filename)
	return a.sendAndReceive(ctx, http.MethodGet, url, nil, false)
}
