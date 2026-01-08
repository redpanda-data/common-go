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
	"testing"
)

func TestMain(t *testing.T) {
	writer = &fsWriter{
		suffix: ".golden",
		write:  os.Getenv("REGENERATE_GOLDEN_FILES") == "true",
		differ: diffChecker(),
	}
	licenseTemplateData = &templateData{
		Organization: defaultOrganization,
		Year:         9999, // pin the year to make sure our tests don't randomly start failing at a year switch
	}

	config := &config{
		Path:             "testdata",
		LicenseDirectory: "testdata/licenses",
		Licenses:         []string{"BSL", "MIT", "RCL", "Apache"},
		Matches: []*match{
			{
				Extension: ".go",
				Type:      "go",
				License:   "BSL",
			},
			{
				Extension: ".yaml",
				Match:     "helm",
				Type:      "helm",
				License:   "MIT",
			},
			{
				Extension: ".yaml",
				Type:      "yaml",
				License:   "RCL",
			},
		},
	}

	if err := config.initializeAndValidate(); err != nil {
		t.Fatal("unexpected error", err)
	}

	if err := doMain(config); err != nil {
		t.Fatal("unexpected error", err)
	}

	if err := writer.differ.error(); err != nil {
		t.Fatal("unexpected error", err)
	}
}
