// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package redpandaotelexporter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	pb "buf.build/gen/go/redpandadata/otel/protocolbuffers/go/redpanda/otel/v1"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sr"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// SerializationFormat specifies the format to use when serializing telemetry data
type SerializationFormat int

const (
	// SerializationFormatJSON uses JSON encoding (default)
	SerializationFormatJSON SerializationFormat = iota
	// SerializationFormatProtobuf uses Protocol Buffers encoding (OTLP format)
	SerializationFormatProtobuf
	// SerializationFormatSchemaRegistryJSON uses JSON encoding with Schema Registry serdes format
	SerializationFormatSchemaRegistryJSON
	// SerializationFormatSchemaRegistryProtobuf uses Protocol Buffers encoding with Schema Registry serdes format
	SerializationFormatSchemaRegistryProtobuf
)

func (f SerializationFormat) isJSON() bool {
	switch f {
	case SerializationFormatJSON, SerializationFormatSchemaRegistryJSON:
		return true
	default:
		return false
	}
}

func (f SerializationFormat) usesSerdes() bool {
	switch f {
	case SerializationFormatSchemaRegistryProtobuf, SerializationFormatSchemaRegistryJSON:
		return true
	default:
		return false
	}
}

// String returns the string representation of the serialization format
func (f SerializationFormat) String() string {
	switch f {
	case SerializationFormatJSON:
		return "json"
	case SerializationFormatProtobuf:
		return "protobuf"
	case SerializationFormatSchemaRegistryJSON:
		return "schema-registry-json"
	case SerializationFormatSchemaRegistryProtobuf:
		return "schema-registry-protobuf"
	default:
		return "unknown"
	}
}

// config holds the configuration for the Kafka exporter.
type config struct {
	brokers             []string
	topic               string
	timeout             time.Duration
	serializationFormat SerializationFormat
	schemaRegistryURL   string // Schema Registry URL for serdes format
	schemaSubject       string
	commonSchemaSubject string
	schemaRegistryOpts  []sr.ClientOpt // Additional Schema Registry client options
	kafkaOptions        []kgo.Opt
}

// Option is a function that configures an Exporter
type Option func(*config)

// WithBrokers sets the Kafka broker addresses (required)
func WithBrokers(brokers ...string) Option {
	return func(c *config) {
		c.brokers = brokers
	}
}

// WithTopic sets the Kafka topic name (overrides the default)
func WithTopic(topic string) Option {
	return func(c *config) {
		c.topic = topic
	}
}

// WithTimeout sets the timeout for export operations (default: 30s)
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) {
		c.timeout = timeout
	}
}

// WithSerializationFormat sets the serialization format (JSON or Protobuf)
func WithSerializationFormat(format SerializationFormat) Option {
	return func(c *config) {
		c.serializationFormat = format
	}
}

// WithSchemaRegistryURL sets the Schema Registry URL for serdes format
func WithSchemaRegistryURL(url string) Option {
	return func(c *config) {
		c.schemaRegistryURL = url
	}
}

// WithSchemaSubject sets the schema subject (overrides default topic naming strategy)
func WithSchemaSubject(subject string) Option {
	return func(c *config) {
		c.schemaSubject = subject
	}
}

// WithCommonSchemaSubject sets the schema subject when using protobuf (overrides the default of topic naming strategy plus -common suffix)
func WithCommonSchemaSubject(subject string) Option {
	return func(c *config) {
		c.commonSchemaSubject = subject
	}
}

// WithSchemaRegistryOptions sets additional franz-go Schema Registry client options
// Use this for authentication, TLS, and other Schema Registry client configuration
func WithSchemaRegistryOptions(opts ...sr.ClientOpt) Option {
	return func(c *config) {
		c.schemaRegistryOpts = opts
	}
}

// WithKafkaOptions sets additional franz-go client options
func WithKafkaOptions(opts ...kgo.Opt) Option {
	return func(c *config) {
		c.kafkaOptions = opts
	}
}

// Exporter is the base type for Kafka OpenTelemetry exporters.
type Exporter struct {
	config         config
	client         *kgo.Client
	schemaRegistry *sr.Client // Schema Registry client (optional if not using SR format)
	schemaCache    map[string]sr.SubjectSchema
	mu             sync.RWMutex
	closed         bool
}

// ensureTopicExists creates the topic if it doesn't already exist.
// It ignores "topic already exists" errors and returns other errors.
func (e *Exporter) ensureTopicExists(ctx context.Context) error {
	admin := kadm.NewClient(e.client)

	// Create topic with default settings:
	// - 3 partitions for parallelism
	// - replication factor of -1 (use broker default)
	// - nil configs (use broker defaults)
	resp, err := admin.CreateTopics(ctx, 3, -1, nil, e.config.topic)
	if err != nil {
		return fmt.Errorf("failed to create topic %s: %w", e.config.topic, err)
	}

	// Check if topic creation succeeded or already exists
	for _, topicResp := range resp {
		if topicResp.Err != nil {
			if !errors.Is(topicResp.Err, kerr.TopicAlreadyExists) {
				return fmt.Errorf("failed to create topic %s: %w", topicResp.Topic, topicResp.Err)
			}
			// Topic already exists, which is fine
		}
	}
	e.client.ForceMetadataRefresh()
	return nil
}

// newExporter creates a new Kafka exporter with the given default topic and options.
// The default topic can be overridden using the WithTopic option.
func newExporter(defaultTopic string, opts ...Option) (*Exporter, error) {
	cfg := config{
		topic:               defaultTopic,
		timeout:             30 * time.Second,
		serializationFormat: SerializationFormatJSON,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.commonSchemaSubject == "" {
		cfg.commonSchemaSubject = DefaultCommonSubject
		if cfg.serializationFormat.isJSON() {
			cfg.commonSchemaSubject = DefaultCommonSubjectJSON
		}
	}

	if len(cfg.brokers) == 0 {
		return nil, errors.New("at least one broker address is required (use WithBrokers)")
	}
	if cfg.topic == "" {
		return nil, errors.New("topic cannot be empty")
	}

	// Validate Schema Registry configuration
	if cfg.serializationFormat == SerializationFormatSchemaRegistryJSON ||
		cfg.serializationFormat == SerializationFormatSchemaRegistryProtobuf {
		if cfg.schemaRegistryURL == "" {
			return nil, errors.New("schema registry URL is required for Schema Registry serdes format (use WithSchemaRegistryURL)")
		}
	}

	// Base options for the Kafka client
	kafkaOpts := []kgo.Opt{
		kgo.SeedBrokers(cfg.brokers...),
		kgo.ClientID("otel-redpanda-exporter"),
		kgo.ProducerBatchMaxBytes(1000000), // 1MB
		kgo.ProducerLinger(100 * time.Millisecond),
		kgo.RequestTimeoutOverhead(5 * time.Second),
	}

	// Append any additional options provided by the user
	kafkaOpts = append(kafkaOpts, cfg.kafkaOptions...)

	client, err := kgo.NewClient(kafkaOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka client: %w", err)
	}

	// Initialize Schema Registry client if using SR format
	var srClient *sr.Client
	if cfg.serializationFormat == SerializationFormatSchemaRegistryJSON ||
		cfg.serializationFormat == SerializationFormatSchemaRegistryProtobuf {
		srOpts := []sr.ClientOpt{sr.URLs(cfg.schemaRegistryURL)}

		// Append any additional options provided by the user (auth, TLS, etc.)
		srOpts = append(srOpts, cfg.schemaRegistryOpts...)

		var err error
		srClient, err = sr.NewClient(srOpts...)
		if err != nil {
			client.Close()
			return nil, fmt.Errorf("failed to create schema registry client: %w", err)
		}
	}

	exporter := &Exporter{
		config:         cfg,
		client:         client,
		schemaRegistry: srClient,
		schemaCache:    make(map[string]sr.SubjectSchema),
	}
	// Create topic if it doesn't exist
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := exporter.ensureTopicExists(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to ensure topic exists: %w", err)
	}

	return exporter, nil
}

// Shutdown closes the exporter and releases resources.
func (e *Exporter) Shutdown(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}

	e.closed = true
	e.client.Close()
	return nil
}

// produceBatch sends multiple records to Kafka
func (e *Exporter) produceBatch(ctx context.Context, records []*kgo.Record) error {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return errors.New("exporter is closed")
	}
	e.mu.RUnlock()

	// Use a context with timeout
	produceCtx, cancel := context.WithTimeout(ctx, e.config.timeout)
	defer cancel()

	results := e.client.ProduceSync(produceCtx, records...)
	if err := results.FirstErr(); err != nil {
		// Check if error is due to unknown topic or partition
		if errors.Is(err, kerr.UnknownTopicOrPartition) {
			// Try to create the topic and retry once
			if createErr := e.ensureTopicExists(ctx); createErr != nil {
				return fmt.Errorf("failed to produce batch to kafka (topic creation failed): %w", err)
			}

			// Retry the produce operation
			produceCtx2, cancel2 := context.WithTimeout(ctx, e.config.timeout)
			defer cancel2()

			results = e.client.ProduceSync(produceCtx2, records...)
			if retryErr := results.FirstErr(); retryErr != nil {
				return fmt.Errorf("failed to produce batch to kafka after topic creation: %w", retryErr)
			}

			return nil
		}

		return fmt.Errorf("failed to produce batch to kafka: %w", err)
	}

	return nil
}

// marshalJSON is a helper to marshal data to JSON
func marshalJSON(msg proto.Message) ([]byte, error) {
	data, err := protojson.MarshalOptions{
		UseProtoNames:  true, // Align with our snake case preferences
		UseEnumNumbers: true, // Closer to the official OTEL JSON format
	}.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to JSON: %w", err)
	}
	return data, nil
}

// marshalProtobuf is a helper to marshal protobuf messages
func marshalProtobuf(msg proto.Message) ([]byte, error) {
	data, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to protobuf: %w", err)
	}
	return data, nil
}

// registerSchema registers a schema with Schema Registry and returns the schema ID.
// The schema ID is cached to avoid repeated registrations.
// This function can be called even when not using Schema Registry serdes format,
// to register schemas for governance/documentation purposes.
func (e *Exporter) registerSchema(ctx context.Context, subject string, schema sr.Schema) (sr.SubjectSchema, error) {
	if e.schemaRegistry == nil {
		return sr.SubjectSchema{}, errors.New("schema registry client not initialized")
	}

	// Check cache first (schema IDs are immutable)
	e.mu.RLock()
	subjectSchema, found := e.schemaCache[subject]
	e.mu.RUnlock()

	if found {
		// Use cached schema ID
		return subjectSchema, nil
	}

	// Register or get existing schema
	// CreateSchema is idempotent - if the schema already exists with the same content, it returns the existing schema
	subjectSchema, err := e.schemaRegistry.CreateSchema(ctx, subject, schema)
	if err != nil {
		return sr.SubjectSchema{}, fmt.Errorf("failed to register schema for subject %s: %w", subject, err)
	}

	// Cache the schema ID (schema IDs are immutable)
	e.mu.Lock()
	e.schemaCache[subject] = subjectSchema
	e.mu.Unlock()

	return subjectSchema, nil
}

// encodeWithSchemaRegistry encodes data with Schema Registry serdes format
// This function registers the schema (if not already registered) and prepends the wire format header
func (e *Exporter) encodeWithSchemaRegistry(ctx context.Context, data []byte, subject string, schema sr.Schema) ([]byte, error) {
	s, err := e.registerSchema(ctx, subject, schema)
	if err != nil {
		return nil, err
	}
	// Use ConfluentHeader to encode with Schema Registry wire format:
	// For JSON: [magic_byte][schema_id][data]
	// For Protobuf: [magic_byte][schema_id][message_indexes][data]
	var header sr.ConfluentHeader
	// For protobuf, we need to include message indexes
	// For a top-level message, this is [0]
	var index []int
	if schema.Type == sr.TypeProtobuf {
		index = []int{0}
	}
	result, err := header.AppendEncode(nil, s.ID, index)
	if err != nil {
		return nil, fmt.Errorf("failed to encode schema registry header: %w", err)
	}
	result = append(result, data...)
	return result, nil
}

func (e *Exporter) signalSubject(msg proto.Message) string {
	if e.config.schemaSubject != "" {
		return e.config.schemaSubject
	}

	switch msg.(type) {
	case *pb.Span:
		if e.config.serializationFormat.isJSON() {
			return DefaultTraceSubjectJSON
		}
		return DefaultTraceSubject
	case *pb.LogRecord:
		if e.config.serializationFormat.isJSON() {
			return DefaultLogSubjectJSON
		}
		return DefaultLogSubject
	case *pb.Metric:
		if e.config.serializationFormat.isJSON() {
			return DefaultMetricSubjectJSON
		}
		return DefaultMetricSubject
	default:
		panic(fmt.Sprintf("unknown message type for schema subject: %T", msg))
	}
}

type signalRecord struct {
	key     []byte
	payload proto.Message
}

func (e *Exporter) export(
	ctx context.Context,
	signals []signalRecord,
	protoSchema string,
	jsonSchema string,
) error {
	if e.config.serializationFormat.usesSerdes() {
		return e.exportSerdes(ctx, signals, protoSchema, jsonSchema)
	}
	return e.exportPlain(ctx, signals, protoSchema, jsonSchema)
}

func (e *Exporter) exportPlain(
	ctx context.Context,
	signals []signalRecord,
	protoSchema string,
	jsonSchema string,
) error {
	if len(signals) == 0 {
		return nil
	}

	// Best effort register schemas in non-serdes mode.
	if e.schemaRegistry != nil {
		if e.config.serializationFormat.isJSON() {
			schema := sr.Schema{
				Schema: jsonSchema,
				Type:   sr.TypeJSON,
			}
			_, _ = e.registerSchema(ctx, e.signalSubject(signals[0].payload), schema)
		} else {
			schema := sr.Schema{
				Schema: otlpCommonProtoSchema,
				Type:   sr.TypeProtobuf,
			}
			s, err := e.registerSchema(ctx, e.config.commonSchemaSubject, schema)
			if err != nil {
				schema = sr.Schema{
					Schema: protoSchema,
					Type:   sr.TypeProtobuf,
					References: []sr.SchemaReference{
						{Name: "redpanda/otel/v1/common.proto", Version: s.Version, Subject: s.Subject},
					},
				}
				_, _ = e.registerSchema(ctx, e.signalSubject(signals[0].payload), schema)
			}
		}
	}
	marshal := marshalProtobuf
	if e.config.serializationFormat.isJSON() {
		marshal = marshalJSON
	}
	records := make([]*kgo.Record, len(signals))
	for i, signal := range signals {
		value, err := marshal(signal.payload)
		if err != nil {
			return err
		}
		records[i] = &kgo.Record{Key: signal.key, Value: value, Topic: e.config.topic}
	}
	return e.produceBatch(ctx, records)
}

func (e *Exporter) exportSerdes(
	ctx context.Context,
	signals []signalRecord,
	protoSchema string,
	jsonSchema string,
) error {
	var marshal func(msg proto.Message) ([]byte, error)
	var schema sr.Schema
	if e.config.serializationFormat.isJSON() {
		marshal = marshalJSON
		schema = sr.Schema{
			Schema: jsonSchema,
			Type:   sr.TypeJSON,
		}
	} else {
		marshal = marshalProtobuf
		s, err := e.registerSchema(ctx, e.config.commonSchemaSubject, sr.Schema{
			Schema: otlpCommonProtoSchema,
			Type:   sr.TypeProtobuf,
		})
		if err != nil {
			return err
		}
		schema = sr.Schema{
			Schema: protoSchema,
			Type:   sr.TypeProtobuf,
			References: []sr.SchemaReference{
				{Name: "redpanda/otel/v1/common.proto", Version: s.Version, Subject: s.Subject},
			},
		}
	}
	records := make([]*kgo.Record, len(signals))
	for i, signal := range signals {
		value, err := marshal(signal.payload)
		if err != nil {
			return err
		}
		value, err = e.encodeWithSchemaRegistry(ctx, value, e.signalSubject(signal.payload), schema)
		if err != nil {
			return err
		}
		records[i] = &kgo.Record{Key: signal.key, Value: value, Topic: e.config.topic}
	}
	return e.produceBatch(ctx, records)
}
