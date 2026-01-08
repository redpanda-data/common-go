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
	"io/fs"
	"os"
)

type fsWriter struct {
	suffix string
	write  bool
	differ *checkDiffs
}

func (f *fsWriter) Write(name string, data []byte, perm fs.FileMode) error {
	name = name + f.suffix

	if f.write {
		return os.WriteFile(name, data, perm)
	}

	if f.differ != nil {
		if err := f.differ.diff(name, data); err != nil {
			return err
		}
	}

	return nil
}
