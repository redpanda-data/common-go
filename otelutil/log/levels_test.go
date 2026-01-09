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

package log

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldLogOTEL(t *testing.T) {
	for name, tt := range map[string]struct {
		shouldLog   []string
		shouldntLog []string
	}{
		"debug": {
			shouldLog: []string{
				"info",
				"debug",
			},
			shouldntLog: []string{
				"verbose",
				"timing",
				"trace",
			},
		},
		"verbose": {
			shouldLog: []string{
				"info",
				"debug",
				"verbose",
				"timing",
				"trace",
			},
		},
		"timing": {
			shouldLog: []string{
				"info",
				"debug",
				"timing",
				"trace",
			},
			shouldntLog: []string{
				"verbose",
			},
		},
		"trace": {
			shouldLog: []string{
				"info",
				"debug",
				"trace",
			},
			shouldntLog: []string{
				"verbose",
				"timing",
			},
		},
		"info": {
			shouldLog: []string{
				"info",
			},
			shouldntLog: []string{
				"debug",
				"verbose",
				"timing",
				"trace",
			},
		},
	} {
		tt := tt
		name := name
		t.Run(name, func(t *testing.T) {
			severity := LevelFromString(name).OTELLevel
			for _, should := range tt.shouldLog {
				require.True(t, shouldLogOTEL(severity, LevelFromString(should).OTELLevel), "should log %q, but doesn't", should)
			}
			for _, shouldnt := range tt.shouldntLog {
				require.False(t, shouldLogOTEL(severity, LevelFromString(shouldnt).OTELLevel), "shouldn't log %q, but does", shouldnt)
			}
		})
	}
}
