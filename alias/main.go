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

package main

import (
	"os"
	"strings"
	"syscall"
)

var AliasTo string

func main() {
	if AliasTo == "" {
		panic("AliasTo not set. Did you miss the `-X main.AliasTo=` build flags?")
	}

	to := strings.Split(AliasTo, " ")
	argv := append(to, os.Args[1:]...) //nolint:gocritic // this is fine

	//nolint:gosec // there's not a safe way to do this.
	if err := syscall.Exec(to[0], argv, os.Environ()); err != nil {
		panic(err)
	}

	panic("unreachable")
}
