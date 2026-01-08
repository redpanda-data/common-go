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
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
	"unicode"
)

func writeLicenseHeader(licenseTemplate *template.Template, templateName string, delimiter delimiter, emptyLine bool, path string, mode os.FileMode, data []byte) error {
	var buf bytes.Buffer
	if err := licenseTemplate.ExecuteTemplate(&buf, templateName, licenseTemplateData); err != nil {
		return err
	}

	var out bytes.Buffer
	if delimiter.Top != "" {
		if _, err := fmt.Fprintln(&out, delimiter.Top); err != nil {
			return err
		}
	}
	s := bufio.NewScanner(&buf)
	for s.Scan() {
		mid := delimiter.Middle
		if mid != "" {
			mid += " "
		}

		if _, err := fmt.Fprintln(&out, strings.TrimRightFunc(mid+s.Text(), unicode.IsSpace)); err != nil {
			return err
		}
	}
	if delimiter.Bottom != "" {
		if _, err := fmt.Fprintln(&out, delimiter.Bottom); err != nil {
			return err
		}
	}

	if emptyLine {
		// ensure a newline
		if _, err := fmt.Fprintln(&out, ""); err != nil {
			return err
		}
	}

	s = bufio.NewScanner(bytes.NewReader(data))
	inLicense := false
	skipEmptyFirstLineAfter := false
	lines := 0
	for s.Scan() {
		lines++

		text := s.Text()

		if lines == 1 {
			if delimiter.Top != "" {
				if s.Text() == delimiter.Top {
					inLicense = true
					skipEmptyFirstLineAfter = true
					continue
				}
			} else if strings.HasPrefix(strings.ReplaceAll(text, " ", ""), delimiter.Middle+"Copyright") {
				inLicense = true
				skipEmptyFirstLineAfter = true
				continue
			}
		} else {
			if inLicense {
				if delimiter.Bottom != "" {
					if s.Text() == delimiter.Bottom {
						inLicense = false
						continue
					}
				} else if !strings.HasPrefix(text, delimiter.Middle) {
					inLicense = false
				}
			}
		}

		if inLicense {
			continue
		}

		if skipEmptyFirstLineAfter {
			skipEmptyFirstLineAfter = false
			if text == "" {
				continue
			}
		}

		if _, err := fmt.Fprintln(&out, text); err != nil {
			return err
		}
	}

	return writer.Write(path, out.Bytes(), mode)
}
