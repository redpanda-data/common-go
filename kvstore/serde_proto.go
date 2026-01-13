package kvstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/sr"
	"google.golang.org/protobuf/proto"
)

// ProtoSerde serializes values as protobuf, optionally with Schema Registry wire format.
type ProtoSerde[T proto.Message] struct {
	new func() T
	sr  *schemaRegistryConfig
}

type schemaRegistryConfig struct {
	schemaID int
}

// ProtoOption configures a ProtoSerde.
type ProtoOption func(*protoConfig)

type protoConfig struct {
	sr *schemaRegistryConfig
}

// Proto returns a protobuf serde for type T.
// The factory function must return a new instance of the proto message.
//
// Example basic usage:
//
//	serde := kvstore.Proto(func() *pb.MyMessage { return &pb.MyMessage{} })
//
// Example with Schema Registry:
//
//	serde := kvstore.Proto(
//	    func() *pb.MyMessage { return &pb.MyMessage{} },
//	    kvstore.WithSchemaRegistry(srClient, "topic-value", protoSchema),
//	)
func Proto[T proto.Message](factory func() T, opts ...ProtoOption) Serde[T] {
	cfg := &protoConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	return &ProtoSerde[T]{
		new: factory,
		sr:  cfg.sr,
	}
}

// Serialize marshals the protobuf message.
// If Schema Registry is configured, wraps with Confluent wire format.
func (s *ProtoSerde[T]) Serialize(v T) ([]byte, error) {
	data, err := proto.Marshal(v)
	if err != nil {
		return nil, err
	}

	// If Schema Registry configured, wrap with wire format
	if s.sr != nil {
		return encodeWithSchemaRegistry(s.sr, data)
	}

	return data, nil
}

// Deserialize unmarshals protobuf bytes to the message.
// If Schema Registry is configured, decodes Confluent wire format first.
func (s *ProtoSerde[T]) Deserialize(b []byte) (T, error) {
	var payload []byte
	var err error

	// If Schema Registry configured, decode wire format
	if s.sr != nil {
		payload, err = decodeFromSchemaRegistry(b)
		if err != nil {
			var zero T
			return zero, err
		}
	} else {
		payload = b
	}

	v := s.new()
	err = proto.Unmarshal(payload, v)
	return v, err
}

// WithSchemaRegistry configures Schema Registry support for ProtoSerde.
// This wraps protobuf payloads with Confluent wire format for schema governance.
// Schema is registered immediately at initialization time.
//
// Parameters:
//   - srClient: Schema Registry client
//   - subject: Subject name (typically "{topic}-value")
//   - schemaContent: Proto file content as string
//   - opts: Optional references for proto imports
//
// Example:
//
//	serde := kvstore.Proto(
//	    func() *pb.MyMessage { return &pb.MyMessage{} },
//	    kvstore.WithSchemaRegistry(srClient, "topic-value", protoSchema),
//	)
func WithSchemaRegistry(
	srClient *sr.Client,
	subject string,
	schemaContent string,
	opts ...SchemaRegistryOption,
) ProtoOption {
	cfg := &srConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Register schema immediately
	schema := sr.Schema{
		Schema:     schemaContent,
		Type:       sr.TypeProtobuf,
		References: cfg.refs,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := srClient.CreateSchema(ctx, subject, schema)
	if err != nil {
		panic(fmt.Sprintf("schema registration for subject %s: %v", subject, err))
	}

	return func(c *protoConfig) {
		c.sr = &schemaRegistryConfig{
			schemaID: result.ID,
		}
	}
}

// SchemaRegistryOption configures Schema Registry behavior.
type SchemaRegistryOption func(*srConfig)

type srConfig struct {
	refs []sr.SchemaReference
}

// WithSchemaReferences specifies proto import dependencies.
//
// Example:
//
//	WithSchemaReferences([]sr.SchemaReference{
//	    {Name: "common.proto", Subject: "topic-common", Version: 1},
//	})
func WithSchemaReferences(refs []sr.SchemaReference) SchemaRegistryOption {
	return func(c *srConfig) {
		c.refs = refs
	}
}

// encodeWithSchemaRegistry wraps protobuf data with Confluent wire format.
//
// Wire format: [magic_byte=0x00][schema_id:4bytes][message_indexes][data]
func encodeWithSchemaRegistry(cfg *schemaRegistryConfig, data []byte) ([]byte, error) {
	var header sr.ConfluentHeader
	result, err := header.AppendEncode(nil, cfg.schemaID, []int{0})
	if err != nil {
		return nil, fmt.Errorf("wire format encode: %w", err)
	}

	return append(result, data...), nil
}

// decodeFromSchemaRegistry extracts protobuf payload from Confluent wire format.
func decodeFromSchemaRegistry(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty data")
	}

	var header sr.ConfluentHeader

	// Decode schema ID
	schemaID, remaining, err := header.DecodeID(b)
	if err != nil {
		return nil, fmt.Errorf("wire format decode schema ID: %w", err)
	}

	_ = schemaID // Available for validation if needed

	// Decode message indexes (for protobuf)
	_, payload, err := header.DecodeIndex(remaining, 10)
	if err != nil {
		return nil, fmt.Errorf("wire format decode indexes: %w", err)
	}

	return payload, nil
}

// NewSchemaRegistrySerde creates a protobuf serde with Schema Registry support.
// Deprecated: Use Proto with WithSchemaRegistry instead.
func NewSchemaRegistrySerde[T proto.Message](
	srClient *sr.Client,
	subject string,
	schemaContent string,
	factory func() T,
	opts ...SchemaRegistryOption,
) Serde[T] {
	return Proto(factory, WithSchemaRegistry(srClient, subject, schemaContent, opts...))
}
