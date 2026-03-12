// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package topicloader provides WatchPolicyFromTopic, the Kafka equivalent of
// loader.WatchPolicyFile. It consumes the compacted policy-sync topic and
// invokes a PolicyCallback each time a new policy record is received.
package topicloader

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/oauth"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/redpanda-data/common-go/authz"
	"github.com/redpanda-data/common-go/authz/loader"
)

// MechanismType selects the SASL authentication mechanism.
type MechanismType int

const (
	// MechanismNone disables SASL authentication (plaintext or TLS only).
	MechanismNone MechanismType = iota
	// MechanismScramSHA256 uses SCRAM-SHA-256.
	MechanismScramSHA256
	// MechanismScramSHA512 uses SCRAM-SHA-512.
	MechanismScramSHA512
	// MechanismServiceAccount uses OAUTHBEARER via OAuth2 client credentials.
	MechanismServiceAccount
)

// OAuthExtension is a custom key-value pair sent in the OAUTHBEARER SASL handshake.
type OAuthExtension struct {
	Key   string
	Value string
}

// SASLConfig selects the authentication mechanism and its credentials.
type SASLConfig struct {
	Mechanism MechanismType

	// SCRAM fields (MechanismScramSHA256 / MechanismScramSHA512)
	Username string
	Password string

	// OAUTHBEARER fields (MechanismServiceAccount)
	ClientID     string
	ClientSecret string
	TokenURL     string
	Scope        string
	Audience     string           // optional; required for Auth0-based providers
	Extensions   []OAuthExtension // optional OAUTHBEARER extensions sent in the SASL handshake
}

// Config holds everything needed to consume the policy-sync Kafka topic.
type Config struct {
	Brokers       []string
	Topic         string // default: "_redpanda.policy-sync"
	ConsumerGroup string
	TLS           *tls.Config // nil = plaintext
	SASL          SASLConfig
}

// WatchPolicyFromTopic consumes the policy-sync Kafka topic and calls cb each
// time a new policy record is received. It blocks until ctx is cancelled.
// Returns a non-nil error only if the Kafka client cannot be constructed.
//
// The consumer starts at the latest offset (AtEnd) and only receives records
// written after it connects.
func WatchPolicyFromTopic(ctx context.Context, cfg Config, cb loader.PolicyCallback) error {
	topic := cfg.Topic
	if topic == "" {
		topic = "_redpanda.policy-sync"
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.ConsumerGroup),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()),
	}

	if cfg.TLS != nil {
		opts = append(opts, kgo.DialTLSConfig(cfg.TLS))
	}

	if mech := buildSASL(ctx, cfg.SASL); mech != nil {
		opts = append(opts, kgo.SASL(mech))
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("failed to create kafka client: %w", err)
	}
	defer client.Close()

	for {
		fetches := client.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}
		fetches.EachError(func(_ string, _ int32, err error) {
			cb(authz.Policy{}, fmt.Errorf("kafka fetch error: %w", err))
		})
		fetches.EachRecord(func(r *kgo.Record) {
			var p authz.Policy
			if err := json.Unmarshal(r.Value, &p); err != nil {
				cb(authz.Policy{}, fmt.Errorf("unmarshal policy record: %w", err))
				return
			}
			cb(p, nil)
		})
	}
}

// buildSASL constructs the appropriate SASL mechanism from the config.
// Returns nil if no mechanism is configured.
func buildSASL(ctx context.Context, cfg SASLConfig) sasl.Mechanism {
	switch cfg.Mechanism {
	case MechanismScramSHA256:
		return scram.Auth{User: cfg.Username, Pass: cfg.Password}.AsSha256Mechanism()
	case MechanismScramSHA512:
		return scram.Auth{User: cfg.Username, Pass: cfg.Password}.AsSha512Mechanism()
	case MechanismServiceAccount:
		ccCfg := clientcredentials.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			TokenURL:     cfg.TokenURL,
		}
		if cfg.Scope != "" {
			ccCfg.Scopes = []string{cfg.Scope}
		}
		if cfg.Audience != "" {
			ccCfg.EndpointParams = map[string][]string{
				"audience": {cfg.Audience},
			}
		}
		extensions := make(map[string]string, len(cfg.Extensions))
		for _, e := range cfg.Extensions {
			extensions[e.Key] = e.Value
		}
		ts := ccCfg.TokenSource(ctx)
		return oauth.Oauth(func(_ context.Context) (oauth.Auth, error) {
			tok, err := ts.Token()
			if err != nil {
				return oauth.Auth{}, fmt.Errorf("fetch oauth token: %w", err)
			}
			return oauth.Auth{Token: tok.AccessToken, Extensions: extensions}, nil
		})
	default:
		return nil
	}
}
