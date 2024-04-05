// Copyright 2020 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package net

// GetKeys gets keys in the map.
func GetKeys(setMap map[string]bool) []string {
	var keys []string
	for key := range setMap {
		keys = append(keys, key)
	}
	return keys
}

// GetIntKeys gets keys in the int-keyed map.
func GetIntKeys(setMap map[int]bool) []int {
	var keys []int
	for key := range setMap {
		keys = append(keys, key)
	}
	return keys
}

// GetKeysFromStringMap gets keys from string map.
func GetKeysFromStringMap(setMap map[string]string) []string {
	var keys []string
	for key := range setMap {
		keys = append(keys, key)
	}
	return keys
}
