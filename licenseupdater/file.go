// Copyright 2026 Redpanda Data, Inc.
//
// Licensed as a Redpanda Enterprise file under the Redpanda Community
// License (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
// https://github.com/redpanda-data/redpanda/blob/master/licenses/rcl.md

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
