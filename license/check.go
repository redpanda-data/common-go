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
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"
)

// Product is a product for which a license is valid.
type Product string

const (
	// add known products here, though we do no validaiton that
	// the license only contains product references of these types

	// ProductConnect represents the connect product.
	ProductConnect Product = "CONNECT"
)

var (
	//go:embed public_key.pem
	licensePublicKeyPem []byte
	defaultChecker      = &checker{
		publicKey: licensePublicKeyPem,
	}
	AllProducts = []Product{
		ProductConnect,
	}
)

type checker struct {
	publicKey []byte
}

// RedpandaLicense is a generic interface for different versions of Redpanda license
// formats with methods for checking license validity.
type RedpandaLicense interface {
	AllowsEnterpriseFeatures() bool
	Expires() time.Time
	IncludesProduct(product Product) bool
}

type licenseVersion struct {
	Version int `json:"version"`
}

func (c *checker) readLicense(file string) (RedpandaLicense, error) {
	licenseFileContents, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read license file: %w", err)
	}

	return c.parseLicense(licenseFileContents)
}

func (c *checker) parseLicense(license []byte) (RedpandaLicense, error) {
	// 1. Try to parse embedded public key
	block, _ := pem.Decode(c.publicKey)
	publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return OpenSourceLicense, fmt.Errorf("failed to parse public key: %w", err)
	}
	publicKeyRSA, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		return OpenSourceLicense, errors.New("failed to parse public key, expected dateFormat is not RSA")
	}

	// Trim Whitespace and Linebreaks for input license
	license = bytes.TrimSpace(license)

	// 2. Split license contents by delimiter
	splitParts := bytes.Split(license, []byte("."))
	if len(splitParts) != 2 {
		return OpenSourceLicense, errors.New("failed to split license contents by delimiter")
	}

	licenseDataEncoded := splitParts[0]
	signatureEncoded := splitParts[1]

	licenseData, err := base64.StdEncoding.DecodeString(string(licenseDataEncoded))
	if err != nil {
		return OpenSourceLicense, fmt.Errorf("failed to decode license data: %w", err)
	}

	signature, err := base64.StdEncoding.DecodeString(string(signatureEncoded))
	if err != nil {
		return OpenSourceLicense, fmt.Errorf("failed to decode license signature: %w", err)
	}
	hash := sha256.Sum256(licenseDataEncoded)

	// 3. Verify license contents with static public key
	if err := rsa.VerifyPKCS1v15(publicKeyRSA, crypto.SHA256, hash[:], signature); err != nil {
		return OpenSourceLicense, fmt.Errorf("failed to verify license signature: %w", err)
	}

	// 4. get the hash of the license that we use for the checksum field later
	checksumHash := sha256.Sum256(license)
	checksum := hex.EncodeToString(checksumHash[:])

	// 5. partially unmarshal the license so we can see what version we're attempting to check
	var partialLicense licenseVersion
	if err := json.Unmarshal(licenseData, &partialLicense); err != nil {
		return OpenSourceLicense, fmt.Errorf("failed to unmarshal license data: %w", err)
	}

	switch partialLicense.Version {
	// 6. If license contents seem to be legit, we will continue unpacking the license
	case 0:
		var rpLicense V0RedpandaLicense
		if err := json.Unmarshal(licenseData, &rpLicense); err != nil {
			return OpenSourceLicense, fmt.Errorf("failed to unmarshal license data: %w", err)
		}
		rpLicense.Checksum = checksum
		return &rpLicense, nil

	case 1:
		var rpLicense V1RedpandaLicense
		if err := json.Unmarshal(licenseData, &rpLicense); err != nil {
			return OpenSourceLicense, fmt.Errorf("failed to unmarshal license data: %w", err)
		}
		rpLicense.Checksum = checksum
		return &rpLicense, nil

	default:
		return OpenSourceLicense, fmt.Errorf("invalid license version: %d", partialLicense.Version)
	}
}

// ReadLicense reads a license from disk and returns the parsed result of either
// a v0 or v1-formatted license.
func ReadLicense(file string) (RedpandaLicense, error) {
	return defaultChecker.readLicense(file)
}

// ParseLicense parses a license from raw bytes and returns the parsed result of either
// a v0 or v1-formatted license.
func ParseLicense(license []byte) (RedpandaLicense, error) {
	return defaultChecker.parseLicense(license)
}

// CheckExppiration returns nil if the expiration timestamp is still valid (not expired). Otherwise,
// it will return an error that provides context when the license expired.
func CheckExpiration(expires time.Time) error {
	if expires.Before(time.Now().UTC()) {
		return fmt.Errorf("license expired on %q", expires.Format(time.RFC3339))
	}
	return nil
}
