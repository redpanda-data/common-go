package kvstore

import (
	"encoding/json"

	"google.golang.org/protobuf/proto"
)

// JSONSerde serializes values as JSON.
type JSONSerde[T any] struct{}

// JSON returns a JSON serde for type T.
func JSON[T any]() Serde[T] {
	return &JSONSerde[T]{}
}

// Serialize marshals the value to JSON.
func (*JSONSerde[T]) Serialize(v T) ([]byte, error) {
	return json.Marshal(v)
}

// Deserialize unmarshals JSON bytes to the value.
func (*JSONSerde[T]) Deserialize(b []byte) (T, error) {
	var v T
	err := json.Unmarshal(b, &v)
	return v, err
}

// ProtoSerde serializes values as protobuf.
type ProtoSerde[T proto.Message] struct {
	new func() T
}

// Proto returns a protobuf serde for type T.
// The factory function must return a new instance of the proto message.
func Proto[T proto.Message](factory func() T) Serde[T] {
	return &ProtoSerde[T]{new: factory}
}

// Serialize marshals the protobuf message.
func (*ProtoSerde[T]) Serialize(v T) ([]byte, error) {
	return proto.Marshal(v)
}

// Deserialize unmarshals protobuf bytes to the message.
func (s *ProtoSerde[T]) Deserialize(b []byte) (T, error) {
	v := s.new()
	err := proto.Unmarshal(b, v)
	return v, err
}
