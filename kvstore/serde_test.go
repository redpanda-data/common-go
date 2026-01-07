package kvstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testUser struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Age   int    `json:"age"`
}

func TestJSONSerde(t *testing.T) {
	serde := JSON[testUser]()

	user := testUser{Email: "alice@example.com", Name: "Alice", Age: 30}

	// Serialize
	data, err := serde.Serialize(user)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"email":"alice@example.com"`)

	// Deserialize
	decoded, err := serde.Deserialize(data)
	require.NoError(t, err)
	assert.Equal(t, user, decoded)
}

func TestJSONSerde_InvalidJSON(t *testing.T) {
	serde := JSON[testUser]()

	_, err := serde.Deserialize([]byte("invalid json"))
	assert.Error(t, err)
}

func TestJSONSerde_Pointer(t *testing.T) {
	serde := JSON[*testUser]()

	user := &testUser{Email: "bob@example.com", Name: "Bob", Age: 25}

	data, err := serde.Serialize(user)
	require.NoError(t, err)

	decoded, err := serde.Deserialize(data)
	require.NoError(t, err)
	assert.Equal(t, user, decoded)
}
