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

package goldenfile

import (
	"bytes"
	"flag"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gonvenience/ytbx"
	"github.com/homeport/dyff/pkg/dyff"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/txtar"
)

// UpdateFlag is the flag used to update golden files, it can be overwritten by packages importing it.
var UpdateFlag = flag.Bool("update-golden", false, "if true, golden assertions will update the expected file instead of performing an assertion")

// Update returns value of the -update-golden CLI flag. A value of true indicates that
// computed files should be updated instead of asserted against.
func Update() bool {
	return *UpdateFlag
}

// Writer wraps a [testing.T] to implement [io.Writer] by utilizing
// [testing.T.Log].
type Writer struct {
	T *testing.T
}

func (w Writer) Write(p []byte) (int, error) {
	w.T.Log(string(p))
	return len(p), nil
}

// GoldenAssertion is the type of assertion to make, YAML, JSON, Text, or Bytes based
type GoldenAssertion int

const (
	// YAML is a yaml-based golden assertion.
	YAML GoldenAssertion = iota
	// JSON is a json-based golden assertion.
	JSON
	// Text is a text-based golden assertion.
	Text
	// Bytes is a bytes-based golden assertion.
	Bytes
)

func assertGolden(t *testing.T, assertionType GoldenAssertion, path string, expected, actual []byte, update func(string, []byte) error) {
	t.Helper()

	if Update() {
		require.NoError(t, update(path, actual))
		return
	}

	const msg = "Divergence from snapshot at %q. If this change is expected re-run this test with -update."

	switch assertionType {
	case Text:
		assert.Equal(t, string(expected), string(actual), msg, path)
	case Bytes:
		assert.Equal(t, expected, actual, msg, path)
	case JSON:
		assert.JSONEq(t, string(expected), string(actual), msg, path)
	case YAML:
		actualDocuments, err := ytbx.LoadDocuments(actual)
		require.NoError(t, err)

		expectedDocuments, err := ytbx.LoadDocuments(expected)
		require.NoError(t, err)

		report, err := dyff.CompareInputFiles(
			ytbx.InputFile{Documents: expectedDocuments},
			ytbx.InputFile{Documents: actualDocuments},
		)
		if err != nil {
			require.NoError(t, err)
		}

		if len(report.Diffs) > 0 {
			hr := dyff.HumanReport{Report: report, OmitHeader: true}

			var buf bytes.Buffer
			require.NoError(t, hr.WriteReport(&buf))

			require.Fail(t, buf.String())
		}

	default:
		require.Fail(t, "unknown assertion type: %#v", assertionType)
	}
}

// AssertGolden is a helper for "golden" or "snapshot" testing. It asserts
// that `actual`, a serialized YAML document, is equal to the one at `path`. If
// `-update` has been passed to `go test`, `actual` will be written to `path`.
func AssertGolden(t *testing.T, assertionType GoldenAssertion, path string, actual []byte) {
	expected, err := os.ReadFile(path) //nolint:gosec // security of file is up to caller
	if !os.IsNotExist(err) {
		require.NoError(t, err)
	}

	assertGolden(t, assertionType, path, expected, actual, func(_ string, _ []byte) error {
		return os.WriteFile(path, actual, 0o644) //nolint:gosec // file permissions are fine for test
	})
}

// TxTarGolden is a wrapper around a txtar archive used as a golden file.
type TxTarGolden struct {
	mu      sync.Mutex
	archive *txtar.Archive
}

// NewTxTar initializes a golden file txtar archive.
func NewTxTar(t *testing.T, path string) *TxTarGolden {
	archive, err := txtar.ParseFile(path)
	if os.IsNotExist(err) {
		archive = &txtar.Archive{}
	} else if err != nil {
		require.NoError(t, err)
	}

	g := &TxTarGolden{archive: archive}

	if Update() {
		t.Cleanup(func() {
			require.NoError(t, g.update(path))
		})
	}

	return g
}

func (g *TxTarGolden) update(path string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	slices.SortFunc(g.archive.Files, func(a, b txtar.File) int {
		return strings.Compare(a.Name, b.Name)
	})

	return os.WriteFile(path, txtar.Format(g.archive), 0o644) //nolint:gosec // file permissions are fine for test
}

func (g *TxTarGolden) getFile(path string) *txtar.File {
	g.mu.Lock()
	defer g.mu.Unlock()

	for i, file := range g.archive.Files {
		if file.Name == path {
			return &g.archive.Files[i]
		}
	}
	g.archive.Files = append(g.archive.Files, txtar.File{
		Name: path,
		Data: []byte{},
	})
	return &g.archive.Files[len(g.archive.Files)-1]
}

// AssertGolden does an assertion on a golden file.
func (g *TxTarGolden) AssertGolden(t *testing.T, assertionType GoldenAssertion, path string, actual []byte) {
	t.Helper()

	file := g.getFile(path)

	assertGolden(t, assertionType, path, file.Data, actual, func(_ string, b []byte) error {
		file.Data = b
		return nil
	})
}
