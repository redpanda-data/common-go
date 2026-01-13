package kvstore

import (
	"encoding/json"
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
