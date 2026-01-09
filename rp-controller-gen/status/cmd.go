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

package status

import (
	"log"
	"os"

	"github.com/spf13/cobra"
)

func Cmd() *cobra.Command {
	var config StatusConfig

	cmd := &cobra.Command{
		Use:     "status",
		Example: "status --base-package github.com/some/repo [--statuses-file ./path/to/statuses.yaml]",
		Run: func(cmd *cobra.Command, args []string) {
			if err := Render(config); err != nil {
				log.Fatal(err)
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&config.StatusesFile, "statuses-file", "statuses.yaml", "The location of the statuses file to read.")
	cmd.Flags().StringVar(&config.Package, "output-package", "statuses", "The go package name.")
	cmd.Flags().StringVar(&config.Outputs.Directory, "output-directory", ".", "The output directory.")
	cmd.Flags().StringVar(&config.Outputs.StatusFile, "output-status-file", "zz_generated_status.go", "The output file for statuses.")
	cmd.Flags().StringVar(&config.Outputs.StatusTestFile, "output-status-test-file", "zz_generated_status_test.go", "The output file for status tests.")
	cmd.Flags().StringVar(&config.Outputs.StateFile, "output-state-file", "zz_generated_state.go", "The output file for state machines.")
	cmd.Flags().StringVar(&config.APIDirectory, "api-directory", "api", "The directory where our api definitions are held.")
	cmd.Flags().StringVar(&config.BasePackage, "base-package", "", "The base package name.")
	cmd.Flags().BoolVar(&config.RewriteComments, "rewrite-comments", false, "If specified, rewrite comments for structures.")

	return cmd
}
