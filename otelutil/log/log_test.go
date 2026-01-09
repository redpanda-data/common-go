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

package log_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"k8s.io/klog/v2"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/redpanda-data/common-go/otelutil/log"
)

func TestSetGlobals(t *testing.T) {
	var buf bytes.Buffer
	logger := logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{}))

	log.SetGlobals(logger)

	klog.Info("Hello from Klog")
	ctrllog.Log.Info("Hello from controller-runtime")
	slog.Info("Hello from slog")
	log.Info(context.TODO(), "Hello from pkg/otelutil/log")

	t.Logf("%s", buf.String())

	require.Contains(t, buf.String(), "Hello from Klog")
	require.Contains(t, buf.String(), "Hello from controller-runtime")
	require.Contains(t, buf.String(), "Hello from slog")
	require.Contains(t, buf.String(), "Hello from pkg/otelutil/log")
}
