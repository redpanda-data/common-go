package rpadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigHandlesUint64AsString(t *testing.T) {
	rawJSON := []byte(`{"big_number": 18446744073709551615}`) // Max uint64 value
	respHandler := func() http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write(rawJSON)
		}
	}
	ts := httptest.NewServer(respHandler())
	defer ts.Close()

	cl, err := NewAdminAPI([]string{ts.URL}, new(NopAuth), nil)
	require.NoError(t, err)

	config, err := cl.Config(context.Background(), true)
	require.NoError(t, err)

	// Verify that the number is retained as a json.Number.
	bigNumber, ok := config["big_number"].(json.Number)
	if !ok {
		t.Fatalf("Expected json.Number, got %T", config["big_number"])
	}

	// Ensure that the value is correct as a string.
	expected := "18446744073709551615"
	require.Equal(t, expected, bigNumber.String(), "Expected %s, got %s", expected, bigNumber.String())

	// This could cause precision loss, but it will not fail.
	if _, err := bigNumber.Float64(); err != nil {
		t.Errorf("Unexpected error converting to float64: %v", err)
	}

	// Conversion to int64 fails (since it's out of int64 range)
	if _, err := bigNumber.Int64(); err == nil {
		t.Errorf("Expected error converting to int64, but got none")
	}
}
