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
	"github.com/spf13/cobra"

	"github.com/redpanda-data/common-go/rp-controller-gen/deprecations"
	"github.com/redpanda-data/common-go/rp-controller-gen/status"
)

func main() {
	root := cobra.Command{
		Use:  "rp-controller-gen",
		Long: "rp-controller-gen is the hub module for hosting various file generation tasks within Redpanda's controllers.",
	}

	root.AddCommand(
		status.Cmd(),
		deprecations.Cmd(),
	)

	if err := root.Execute(); err != nil {
		panic(err)
	}
}
