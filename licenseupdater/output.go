// Copyright 2026 Redpanda Data, Inc.
//
// Licensed as a Redpanda Enterprise file under the Redpanda Community
// License (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
// https://github.com/redpanda-data/redpanda/blob/master/licenses/rcl.md

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
	name += f.suffix

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
