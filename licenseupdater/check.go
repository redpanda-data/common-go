// Copyright 2026 Redpanda Data, Inc.
//
// Licensed as a Redpanda Enterprise file under the Redpanda Community
// License (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
// https://github.com/redpanda-data/redpanda/blob/master/licenses/rcl.md

package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/cockroachdb/errors"
	"github.com/sergi/go-diff/diffmatchpatch"
)

type diff struct {
	path  string
	diffs []diffmatchpatch.Diff
}

func (d *diff) string(differ *diffmatchpatch.DiffMatchPatch) string {
	diff := differ.DiffPrettyText(d.diffs)
	return fmt.Sprintf("%s:\n%s", d.path, diff)
}

type checkDiffs struct {
	differ *diffmatchpatch.DiffMatchPatch
	diffs  []diff
	mutex  sync.RWMutex
}

func diffChecker() *checkDiffs {
	differ := diffmatchpatch.New()
	return &checkDiffs{
		differ: differ,
	}
}

func (c *checkDiffs) diff(path string, newData []byte) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if !bytes.Equal(data, newData) {
		diffs := c.differ.DiffMain(string(data), string(newData), false)
		c.mutex.Lock()
		c.diffs = append(c.diffs, diff{path, diffs})
		c.mutex.Unlock()
	}

	return nil
}

func (c *checkDiffs) error() error {
	c.mutex.RLock()

	if len(c.diffs) == 0 {
		c.mutex.RUnlock()
		return nil
	}

	diffs := make([]diff, len(c.diffs))
	copy(diffs, c.diffs)
	c.mutex.RUnlock()

	sort.SliceStable(diffs, func(i, j int) bool {
		a, b := diffs[i], diffs[j]
		return a.path < b.path
	})

	errs := []string{}
	for _, diff := range diffs {
		errs = append(errs, diff.string(c.differ))
	}

	return errors.New(strings.Join(errs, "\n"))
}
