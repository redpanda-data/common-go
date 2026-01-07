// Copyright 2025 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package license

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func createLicense(t *testing.T, license any) ([]byte, []byte) {
	t.Helper()

	licenseBytes, err := json.Marshal(license)
	require.NoError(t, err)

	licenseBytesEncoded := base64.StdEncoding.AppendEncode(nil, bytes.TrimSpace(licenseBytes))
	licenseEncodedBytesHash := sha256.Sum256(licenseBytesEncoded)

	privKeyRSA, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKeyMarshalled, err := x509.MarshalPKIXPublicKey(&privKeyRSA.PublicKey)
	require.NoError(t, err)

	var pemBuf bytes.Buffer
	require.NoError(t, pem.Encode(&pemBuf, &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyMarshalled,
	}))

	signature, err := rsa.SignPKCS1v15(nil, privKeyRSA, crypto.SHA256, licenseEncodedBytesHash[:])
	require.NoError(t, err)

	return pemBuf.Bytes(), []byte(string(licenseBytesEncoded) + "." + base64.StdEncoding.EncodeToString(signature))
}

func TestV0Licenses(t *testing.T) {
	for _, test := range []struct {
		Name         string
		License      *V0RedpandaLicense
		IsEnterprise bool
	}{
		{
			Name: "expired license",
			License: &V0RedpandaLicense{
				Version:      0,
				Organization: "meow",
				Type:         V0LicenseTypeEnterprise,
				Expiry:       time.Now().Add(-time.Hour).Unix(),
			},
			IsEnterprise: false,
		},
		{
			Name: "open source license",
			License: &V0RedpandaLicense{
				Version:      0,
				Organization: "meow",
				Type:         V0LicenseTypeOpenSource,
				Expiry:       time.Now().Add(time.Hour).Unix(),
			},
			IsEnterprise: false,
		},
		{
			Name: "free trial license",
			License: &V0RedpandaLicense{
				Version:      0,
				Organization: "meow",
				Type:         V0LicenseTypeFreeTrial,
				Expiry:       time.Now().Add(time.Hour).Unix(),
			},
			IsEnterprise: true,
		},
		{
			Name: "enterprise license",
			License: &V0RedpandaLicense{
				Version:      0,
				Organization: "meow",
				Type:         V0LicenseTypeEnterprise,
				Expiry:       time.Now().Add(time.Hour).Unix(),
			},
			IsEnterprise: true,
		},
	} {
		t.Run(test.Name, func(t *testing.T) {
			pubKey, license := createLicense(t, test.License)

			checker := &checker{publicKey: pubKey}
			parsed, err := checker.parseLicense(license)
			require.NoError(t, err)

			require.Equal(t, test.IsEnterprise, parsed.AllowsEnterpriseFeatures())
			for _, product := range AllProducts {
				require.True(t, parsed.IncludesProduct(product))
			}
		})
	}
}

func TestV1Licenses(t *testing.T) {
	for _, test := range []struct {
		Name         string
		License      *V1RedpandaLicense
		IsEnterprise bool
	}{
		{
			Name: "expired license",
			License: &V1RedpandaLicense{
				Version:      1,
				Organization: "meow",
				Type:         LicenseTypeEnterprise,
				Expiry:       time.Now().Add(-time.Hour).Unix(),
			},
			IsEnterprise: false,
		},
		{
			Name: "open source license",
			License: &V1RedpandaLicense{
				Version:      1,
				Organization: "meow",
				Type:         LicenseTypeOpenSource,
				Expiry:       time.Now().Add(time.Hour).Unix(),
			},
			IsEnterprise: false,
		},
		{
			Name: "free trial license",
			License: &V1RedpandaLicense{
				Version:      1,
				Organization: "meow",
				Type:         LicenseTypeFreeTrial,
				Expiry:       time.Now().Add(time.Hour).Unix(),
			},
			IsEnterprise: true,
		},
		{
			Name: "enterprise license",
			License: &V1RedpandaLicense{
				Version:      1,
				Organization: "meow",
				Type:         LicenseTypeEnterprise,
				Expiry:       time.Now().Add(time.Hour).Unix(),
				Products:     []Product{ProductConnect},
			},
			IsEnterprise: true,
		},
	} {
		t.Run(test.Name, func(t *testing.T) {
			pubKey, license := createLicense(t, test.License)

			checker := &checker{publicKey: pubKey}
			parsed, err := checker.parseLicense(license)
			require.NoError(t, err)

			require.Equal(t, test.IsEnterprise, parsed.AllowsEnterpriseFeatures())
			for _, product := range AllProducts {
				if slices.Contains(test.License.Products, product) {
					require.True(t, parsed.IncludesProduct(product))
				} else {
					require.False(t, parsed.IncludesProduct(product))
				}
			}
		})
	}
}
