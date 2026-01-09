// Copyright 2026 Redpanda Data, Inc.
//
// Licensed as a Redpanda Enterprise file under the Redpanda Community
// License (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
// https://github.com/redpanda-data/redpanda/blob/master/licenses/rcl.md

package main

type delimiter struct {
	Top    string `yaml:"top"`
	Middle string `yaml:"middle"`
	Bottom string `yaml:"bottom"`
}

var (
	emptyDelimiter    = delimiter{}
	builtinDelimiters = map[string]delimiter{
		"go":   {Middle: "//"},
		"yaml": {Middle: "#"},
		"helm": {Top: "{{/*", Bottom: "*/}}"},
	}
)
