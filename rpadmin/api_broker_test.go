// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package rpadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreRestartProbe(t *testing.T) {
	want := PreRestartCheckResult{
		Risks: RestartRisks{
			RF1Offline:                 []string{"kafka/topic_a/0"},
			FullAcksProduceUnavailable: []string{},
			Unavailable:                []string{},
			Acks1DataLoss:              []string{},
		},
	}

	for name, tc := range map[string]struct {
		limit     int
		wantQuery string
	}{
		"no limit uses server default": {
			limit:     0,
			wantQuery: "",
		},
		"limit is propagated as query param": {
			limit:     32,
			wantQuery: "limit=32",
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v1/broker/pre_restart_probe", r.URL.Path)
				assert.Equal(t, tc.wantQuery, r.URL.RawQuery)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(want)
			}))
			defer srv.Close()

			client, err := NewAdminAPI([]string{srv.URL}, new(NopAuth), nil)
			require.NoError(t, err)

			got, err := client.PreRestartProbe(context.Background(), tc.limit)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

// TestPreRestartProbe_RiskCategoriesUnmarshal pins the JSON field names
// so a typo on either side of the wire becomes a test failure rather
// than a silently-empty risk list.
func TestPreRestartProbe_RiskCategoriesUnmarshal(t *testing.T) {
	body := `{
		"risks": {
			"rf1_offline": ["kafka/topic_a/0"],
			"full_acks_produce_unavailable": ["kafka/topic_b/3"],
			"unavailable": ["kafka/topic_c/7"],
			"acks1_data_loss": ["kafka/topic_d/2"]
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client, err := NewAdminAPI([]string{srv.URL}, new(NopAuth), nil)
	require.NoError(t, err)

	got, err := client.PreRestartProbe(context.Background(), 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"kafka/topic_a/0"}, got.Risks.RF1Offline)
	assert.Equal(t, []string{"kafka/topic_b/3"}, got.Risks.FullAcksProduceUnavailable)
	assert.Equal(t, []string{"kafka/topic_c/7"}, got.Risks.Unavailable)
	assert.Equal(t, []string{"kafka/topic_d/2"}, got.Risks.Acks1DataLoss)
}

func TestPostRestartProbe(t *testing.T) {
	want := PostRestartCheckResult{LoadReclaimedPercent: 87}

	for name, tc := range map[string]struct {
		limit     int
		wantQuery string
	}{
		"no limit": {limit: 0, wantQuery: ""},
		"limit propagated": {limit: 64, wantQuery: "limit=64"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v1/broker/post_restart_probe", r.URL.Path)
				assert.Equal(t, tc.wantQuery, r.URL.RawQuery)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(want)
			}))
			defer srv.Close()

			client, err := NewAdminAPI([]string{srv.URL}, new(NopAuth), nil)
			require.NoError(t, err)

			got, err := client.PostRestartProbe(context.Background(), tc.limit)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}
