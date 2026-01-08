// Copyright 2025 Redpanda Data, Inc.
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
	"text/template"
)

type matchedFile struct {
	path      string
	mode      os.FileMode
	delimiter delimiter
	license   string
	short     bool
}

func (f *matchedFile) process() error {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return err
	}

	var licenseTemplate *template.Template
	var templateName string
	if f.short {
		licenseTemplate = getShortHeaderLicenseTemplate(f.license)
		templateName = shortHeaderName(f.license)
	} else {
		licenseTemplate = getHeaderLicenseTemplate(f.license)
		templateName = headerName(f.license)
	}

	return writeLicenseHeader(licenseTemplate, templateName, f.delimiter, true, f.path, f.mode, data)
}
