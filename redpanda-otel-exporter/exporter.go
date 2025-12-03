package redpandaotelexporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
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
	clientID            string
	timeout             time.Duration
	resource            *resource.Resource
	serializationFormat SerializationFormat
	schemaRegistryURL   string         // Schema Registry URL for serdes format
	schemaSubject       string         // Optional schema subject override (defaults to topic naming strategy)
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

// WithClientID sets the Kafka client ID (default: "otel-kafka-exporter")
func WithClientID(clientID string) Option {
	return func(c *config) {
		c.clientID = clientID
	}
}

// WithTimeout sets the timeout for export operations (default: 30s)
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) {
		c.timeout = timeout
	}
}

// WithResource sets the OpenTelemetry resource
func WithResource(resource *resource.Resource) Option {
	return func(c *config) {
		c.resource = resource
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
	schemaRegistry *sr.Client     // Schema Registry client (nil if not using SR format)
	schemaIDCache  map[string]int // Cache of schema IDs by subject name
	mu             sync.RWMutex
	closed         bool
}

// newExporter creates a new Kafka exporter with the given default topic and options.
// The default topic can be overridden using the WithTopic option.
func newExporter(defaultTopic string, opts ...Option) (*Exporter, error) {
	cfg := config{
		topic:               defaultTopic,
		clientID:            "otel-kafka-exporter",
		timeout:             30 * time.Second,
		serializationFormat: SerializationFormatJSON,
	}

	for _, opt := range opts {
		opt(&cfg)
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
		kgo.ClientID(cfg.clientID),
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

	return &Exporter{
		config:         cfg,
		client:         client,
		schemaRegistry: srClient,
		schemaIDCache:  make(map[string]int),
	}, nil
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
		return fmt.Errorf("failed to produce batch to kafka: %w", err)
	}

	return nil
}

// attributeValue converts an attribute.Value to OTLP JSON AnyValue format
// Per OTLP spec, values must be wrapped in typed fields like stringValue, intValue, etc.
func attributeValue(v attribute.Value) map[string]any {
	switch v.Type() {
	case attribute.BOOL:
		return map[string]any{"boolValue": v.AsBool()}
	case attribute.INT64:
		return map[string]any{"intValue": fmt.Sprintf("%d", v.AsInt64())}
	case attribute.FLOAT64:
		return map[string]any{"doubleValue": v.AsFloat64()}
	case attribute.BOOLSLICE:
		bools := v.AsBoolSlice()
		arrayValues := make([]map[string]any, len(bools))
		for i, b := range bools {
			arrayValues[i] = map[string]any{"boolValue": b}
		}
		return map[string]any{"arrayValue": map[string]any{"values": arrayValues}}
	case attribute.INT64SLICE:
		ints := v.AsInt64Slice()
		arrayValues := make([]map[string]any, len(ints))
		for i, n := range ints {
			arrayValues[i] = map[string]any{"intValue": fmt.Sprintf("%d", n)}
		}
		return map[string]any{"arrayValue": map[string]any{"values": arrayValues}}
	case attribute.FLOAT64SLICE:
		floats := v.AsFloat64Slice()
		arrayValues := make([]map[string]any, len(floats))
		for i, f := range floats {
			arrayValues[i] = map[string]any{"doubleValue": f}
		}
		return map[string]any{"arrayValue": map[string]any{"values": arrayValues}}
	case attribute.STRINGSLICE:
		strings := v.AsStringSlice()
		arrayValues := make([]map[string]any, len(strings))
		for i, s := range strings {
			arrayValues[i] = map[string]any{"stringValue": s}
		}
		return map[string]any{"arrayValue": map[string]any{"values": arrayValues}}
	default:
		// Handles attribute.STRING and any other unknown types by converting to string
		return map[string]any{"stringValue": v.AsString()}
	}
}

// attributesToArray converts a slice of attributes to an array of {key, value} objects
// This follows the OTLP JSON encoding where attributes are represented as an array
func attributesToArray(attrs []attribute.KeyValue) []map[string]any {
	result := make([]map[string]any, len(attrs))
	for i, attr := range attrs {
		result[i] = map[string]any{
			"key":   string(attr.Key),
			"value": attributeValue(attr.Value),
		}
	}
	return result
}

// marshalJSON is a helper to marshal data to JSON
func marshalJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
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

// getSubjectName returns the schema subject name to use.
// If schemaSubject is configured, it uses that.
// Otherwise, it returns "{topic}-value" following Confluent Schema Registry naming convention.
func (e *Exporter) getSubjectName() string {
	if e.config.schemaSubject != "" {
		return e.config.schemaSubject
	}
	return e.config.topic + "-value"
}

// registerSchema registers a schema with Schema Registry and returns the schema ID.
// The schema ID is cached to avoid repeated registrations.
// This function can be called even when not using Schema Registry serdes format,
// to register schemas for governance/documentation purposes.
func (e *Exporter) registerSchema(ctx context.Context, subject string, schema sr.Schema) (int, error) {
	if e.schemaRegistry == nil {
		return 0, errors.New("schema registry client not initialized")
	}

	// Check cache first (schema IDs are immutable)
	e.mu.RLock()
	cachedID, found := e.schemaIDCache[subject]
	e.mu.RUnlock()

	if found {
		// Use cached schema ID
		return cachedID, nil
	}

	// Register or get existing schema
	// CreateSchema is idempotent - if the schema already exists with the same content, it returns the existing schema
	subjectSchema, err := e.schemaRegistry.CreateSchema(ctx, subject, schema)
	if err != nil {
		return 0, fmt.Errorf("failed to register schema for subject %s: %w", subject, err)
	}

	schemaID := subjectSchema.ID

	// Cache the schema ID (schema IDs are immutable)
	e.mu.Lock()
	e.schemaIDCache[subject] = schemaID
	e.mu.Unlock()

	return schemaID, nil
}

// encodeWithSchemaRegistry encodes data with Schema Registry serdes format
// This function registers the schema (if not already registered) and prepends the wire format header
func (e *Exporter) encodeWithSchemaRegistry(ctx context.Context, data []byte, subject string, schema sr.Schema) ([]byte, error) {
	// Register schema and get ID
	schemaID, err := e.registerSchema(ctx, subject, schema)
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

	// AppendEncode adds the Confluent wire format header and appends the data
	result, err := header.AppendEncode(nil, schemaID, index)
	if err != nil {
		return nil, fmt.Errorf("failed to encode schema registry header: %w", err)
	}

	// Append the actual data
	result = append(result, data...)

	return result, nil
}

// resourceToHeaders converts resource attributes to Kafka record headers
func resourceToHeaders(r *resource.Resource) []kgo.RecordHeader {
	if r == nil {
		return nil
	}

	attrs := r.Attributes()
	headers := make([]kgo.RecordHeader, 0, len(attrs))
	for _, attr := range attrs {
		// Convert attribute value to string for header
		var value string
		switch attr.Value.Type() {
		case attribute.BOOL:
			value = fmt.Sprintf("%t", attr.Value.AsBool())
		case attribute.INT64:
			value = fmt.Sprintf("%d", attr.Value.AsInt64())
		case attribute.FLOAT64:
			value = fmt.Sprintf("%f", attr.Value.AsFloat64())
		default:
			// Handles attribute.STRING and any other types by converting to string
			value = attr.Value.AsString()
		}

		headers = append(headers, kgo.RecordHeader{
			Key:   string(attr.Key),
			Value: []byte(value),
		})
	}

	return headers
}
