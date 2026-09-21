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

package goldenfile_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/txtar"

	"github.com/redpanda-data/common-go/goldenfile"
)

// setUpdate forces [goldenfile.Update] to report true for the duration of t.
func setUpdate(t *testing.T) {
	prev := goldenfile.UpdateFlag
	update := true
	goldenfile.UpdateFlag = &update
	t.Cleanup(func() { goldenfile.UpdateFlag = prev })
}

func TestTxTarGoldenParallel(t *testing.T) {
	const entries = 50

	path := filepath.Join(t.TempDir(), "golden.txtar")

	// NB: -update-golden is global state, so these two phases can't be t.Parallel'd
	// against each other. The subtests within each phase are.
	t.Run("update", func(t *testing.T) {
		setUpdate(t)

		golden := goldenfile.NewTxTar(t, path)

		for i := range entries {
			t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
				t.Parallel()

				golden.AssertGolden(t, goldenfile.Text, fmt.Sprintf("%02d.txt", i), []byte(fmt.Sprintf("value %d\n", i)))
			})
		}
	})

	archive, err := txtar.ParseFile(path)
	require.NoError(t, err)

	names := make([]string, 0, len(archive.Files))
	for i, file := range archive.Files {
		names = append(names, file.Name)
		require.Equal(t, fmt.Sprintf("%02d.txt", i), file.Name)
		require.Equal(t, fmt.Sprintf("value %d\n", i), string(file.Data))
	}

	require.Len(t, names, entries)
	require.True(t, slices.IsSorted(names), "archive entries must be written in sorted order: %v", names)

	t.Run("assert", func(t *testing.T) {
		golden := goldenfile.NewTxTar(t, path)

		for i := range entries {
			t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
				t.Parallel()

				golden.AssertGolden(t, goldenfile.Text, fmt.Sprintf("%02d.txt", i), []byte(fmt.Sprintf("value %d\n", i)))
			})
		}
	})
}

func TestAssertGoldenParallel(t *testing.T) {
	const entries = 20

	dir := t.TempDir()

	t.Run("update", func(t *testing.T) {
		setUpdate(t)

		for i := range entries {
			t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
				t.Parallel()

				goldenfile.AssertGolden(t, goldenfile.Text, filepath.Join(dir, fmt.Sprintf("%02d.txt", i)), []byte(fmt.Sprintf("value %d\n", i)))
			})
		}
	})

	for i := range entries {
		contents, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%02d.txt", i)))
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("value %d\n", i), string(contents))
	}

	for i := range entries {
		t.Run(fmt.Sprintf("assert/%02d", i), func(t *testing.T) {
			t.Parallel()

			goldenfile.AssertGolden(t, goldenfile.Text, filepath.Join(dir, fmt.Sprintf("%02d.txt", i)), []byte(fmt.Sprintf("value %d\n", i)))
		})
	}
}

// TestTxTarGoldenMissingEntry asserts that an absent entry compares equal to empty
// content for every assertion type, notably Bytes, where assert.Equal treats a nil
// []byte and an empty one as unequal.
func TestTxTarGoldenMissingEntry(t *testing.T) {
	golden := goldenfile.NewTxTar(t, filepath.Join(t.TempDir(), "absent.txtar"))

	golden.AssertGolden(t, goldenfile.Bytes, "missing.txt", []byte{})
	golden.AssertGolden(t, goldenfile.Text, "missing.txt", nil)
}

// TestTxTarGoldenClonesInput asserts that a caller reusing its buffer can't corrupt
// already recorded entries.
func TestTxTarGoldenClonesInput(t *testing.T) {
	setUpdate(t)

	path := filepath.Join(t.TempDir(), "golden.txtar")

	t.Run("write", func(t *testing.T) {
		golden := goldenfile.NewTxTar(t, path)

		buf := []byte("first\n")
		golden.AssertGolden(t, goldenfile.Text, "a.txt", buf)
		copy(buf, "second")
	})

	archive, err := txtar.ParseFile(path)
	require.NoError(t, err)
	require.Len(t, archive.Files, 1)
	require.Equal(t, "first\n", string(archive.Files[0].Data))
}
