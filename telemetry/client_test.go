// Copyright 2026 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"
)

func genKeyPEM(t *testing.T) (privPEM []byte, pub *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	privPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return privPEM, &key.PublicKey
}

func TestClientSendSignsAndPosts(t *testing.T) {
	privPEM, pub := genKeyPEM(t)

	var gotPath, gotUA, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUA = r.Header.Get("User-Agent")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{
		Endpoint:      srv.URL,
		Path:          "/kubernetes",
		UserAgent:     "RedpandaOperator/test",
		SigningKeyPEM: privPEM,
	})
	require.NoError(t, err)
	require.False(t, c.Disabled())

	type payload struct {
		ID  string `json:"id"`
		Foo int    `json:"foo"`
	}
	require.NoError(t, c.Send(context.Background(), payload{ID: "abc", Foo: 7}))

	require.Equal(t, "/kubernetes", gotPath)
	require.Equal(t, "RedpandaOperator/test", gotUA)
	require.Equal(t, "text/plain", gotCT)

	tok, err := josejwt.ParseSigned(string(gotBody), []jose.SignatureAlgorithm{jose.RS256})
	require.NoError(t, err)
	var claims struct {
		ID  string `json:"id"`
		Foo int    `json:"foo"`
	}
	require.NoError(t, tok.Claims(pub, &claims))
	require.Equal(t, "abc", claims.ID)
	require.Equal(t, 7, claims.Foo)
}

func TestClientAcceptsPKCS1Key(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	pub := &key.PublicKey

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{
		Endpoint:      srv.URL,
		Path:          "/kubernetes",
		UserAgent:     "RedpandaOperator/test",
		SigningKeyPEM: privPEM,
	})
	require.NoError(t, err)
	require.False(t, c.Disabled())

	type payload struct {
		ID string `json:"id"`
	}
	require.NoError(t, c.Send(context.Background(), payload{ID: "pkcs1"}))

	tok, err := josejwt.ParseSigned(string(gotBody), []jose.SignatureAlgorithm{jose.RS256})
	require.NoError(t, err)
	var claims struct {
		ID string `json:"id"`
	}
	require.NoError(t, tok.Claims(pub, &claims))
	require.Equal(t, "pkcs1", claims.ID)
}

func TestClientAppliesJWTHeaders(t *testing.T) {
	privPEM, pub := genKeyPEM(t)

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{
		Endpoint:      srv.URL,
		Path:          "/kubernetes",
		SigningKeyPEM: privPEM,
		JWTHeaders:    map[string]any{"key_generation": 1},
	})
	require.NoError(t, err)

	type payload struct {
		A string `json:"a"`
	}
	require.NoError(t, c.Send(context.Background(), payload{A: "b"}))

	tok, err := josejwt.ParseSigned(string(gotBody), []jose.SignatureAlgorithm{jose.RS256})
	require.NoError(t, err)
	require.NotEmpty(t, tok.Headers)
	// The parsed protected header carries the extra JWT headers configured via
	// JWTHeaders. The numeric value is decoded as a JSON number, so assert on
	// its presence and string form robustly.
	v, ok := tok.Headers[0].ExtraHeaders[jose.HeaderKey("key_generation")]
	require.True(t, ok, "expected key_generation header to be present")
	require.Equal(t, "1", fmt.Sprint(v))

	// Sanity check the claims still verify with the public key.
	var claims payload
	require.NoError(t, tok.Claims(pub, &claims))
	require.Equal(t, "b", claims.A)
}

func TestClientSendErrorsOnNon2xx(t *testing.T) {
	privPEM, _ := genKeyPEM(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, err := New(Config{Endpoint: srv.URL, Path: "/x", SigningKeyPEM: privPEM, RetryCount: 0})
	require.NoError(t, err)
	require.Error(t, c.Send(context.Background(), map[string]string{"a": "b"}))
}

func TestClientDisabledWhenNoKey(t *testing.T) {
	c, err := New(Config{Endpoint: "http://127.0.0.1:0", Path: "/x"})
	require.NoError(t, err)
	require.True(t, c.Disabled())
	require.NoError(t, c.Send(context.Background(), map[string]string{"a": "b"}))
}
