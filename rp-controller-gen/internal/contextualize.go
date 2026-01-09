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

package internal

import (
	"errors"
	"go/scanner"
	"strconv"
	"strings"
)

func ContextualizeFormatErrors(data []byte, err error) string {
	var serr scanner.ErrorList
	if errors.As(err, &serr) {
		errContext := []string{}
		lines := strings.Split(string(data), "\n")

		for i, err := range serr {
			line := err.Pos.Line

			lineContext := []string{"[ERROR " + strconv.Itoa(i+1) + "]:\n"}
			if line-2 >= 0 {
				lineContext = append(lineContext, lines[line-2])
			}
			lineContext = append(lineContext, lines[line-1])
			if line < len(lines) {
				lineContext = append(lineContext, lines[line])
			}
			errContext = append(errContext, strings.Join(lineContext, "\n"))
		}
		return strings.Join(errContext, "\n\n")
	}

	return ""
}
