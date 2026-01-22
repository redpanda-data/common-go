// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package authz_test

import (
	"testing"

	"github.com/redpanda-data/common-go/authz"
)

func TestResourceFullName_Name(t *testing.T) {
	tests := []struct {
		name     string
		resource authz.ResourceName
		want     authz.ResourceID
	}{
		{
			name:     "full resource path",
			resource: "organizations/acme/resourcegroups/foo/dataplanes/bar/mcpservers/qux",
			want:     "qux",
		},
		{
			name:     "short path",
			resource: "organizations/acme",
			want:     "acme",
		},
		{
			name:     "single element",
			resource: "foo",
			want:     "foo",
		},
		{
			name:     "empty resource",
			resource: "",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.resource.Name()
			if got != tt.want {
				t.Errorf("ResourceFullName.Name() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResourceFullName_Type(t *testing.T) {
	tests := []struct {
		name     string
		resource authz.ResourceName
		want     authz.ResourceType
	}{
		{
			name:     "full resource path",
			resource: "organizations/acme/resourcegroups/foo/dataplanes/bar/mcpservers/qux",
			want:     "mcpservers",
		},
		{
			name:     "short path",
			resource: "organizations/acme",
			want:     "organizations",
		},
		{
			name:     "single element",
			resource: "foo",
			want:     "",
		},
		{
			name:     "empty resource",
			resource: "",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.resource.Type()
			if got != tt.want {
				t.Errorf("ResourceFullName.Type() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResourceFullName_Parent(t *testing.T) {
	tests := []struct {
		name     string
		resource authz.ResourceName
		want     authz.ResourceName
	}{
		{
			name:     "full resource path",
			resource: "organizations/acme/resourcegroups/foo/dataplanes/bar/mcpservers/qux",
			want:     "organizations/acme/resourcegroups/foo/dataplanes/bar",
		},
		{
			name:     "short path",
			resource: "organizations/acme/resourcegroups/foo",
			want:     "organizations/acme",
		},
		{
			name:     "minimal path",
			resource: "organizations/acme",
			want:     "",
		},
		{
			name:     "empty resource",
			resource: "",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.resource.Parent()
			if got != tt.want {
				t.Errorf("ResourceFullName.Parent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResourceFullName_Child(t *testing.T) {
	tests := []struct {
		name         string
		resource     authz.ResourceName
		resourceType authz.ResourceType
		resourceName authz.ResourceID
		want         authz.ResourceName
	}{
		{
			name:         "add child to full path",
			resource:     "organizations/acme/resourcegroups/foo/dataplanes/bar",
			resourceType: "mcpservers",
			resourceName: "qux",
			want:         "organizations/acme/resourcegroups/foo/dataplanes/bar/mcpservers/qux",
		},
		{
			name:         "add child to short path",
			resource:     "organizations/acme",
			resourceType: "resourcegroups",
			resourceName: "foo",
			want:         "organizations/acme/resourcegroups/foo",
		},
		{
			name:         "add child to empty resource",
			resource:     "",
			resourceType: "organizations",
			resourceName: "acme",
			want:         "organizations/acme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.resource.Child(tt.resourceType, tt.resourceName)
			if got != tt.want {
				t.Errorf("ResourceFullName.Child() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResourceName_Relative(t *testing.T) {
	tests := []struct {
		name      string
		resource  authz.ResourceName
		ancestor  authz.ResourceName
		wantName  authz.ResourceName
		wantChild bool
	}{
		{
			name:      "deep resource with mid-level ancestor",
			resource:  "organizations/acme/resourcegroups/foo/dataplanes/bar/mcpservers/qux",
			ancestor:  "organizations/acme/resourcegroups/foo",
			wantName:  "dataplanes/bar/mcpservers/qux",
			wantChild: true,
		},
		{
			name:      "deep resource with top-level ancestor",
			resource:  "organizations/acme/resourcegroups/foo/dataplanes/bar/mcpservers/qux",
			ancestor:  "organizations/acme",
			wantName:  "resourcegroups/foo/dataplanes/bar/mcpservers/qux",
			wantChild: true,
		},
		{
			name:      "resource with immediate parent",
			resource:  "organizations/acme/resourcegroups/foo/dataplanes/bar",
			ancestor:  "organizations/acme/resourcegroups/foo",
			wantName:  "dataplanes/bar",
			wantChild: true,
		},
		{
			name:      "resource relative to root (empty ancestor)",
			resource:  "organizations/acme/resourcegroups/foo",
			ancestor:  "",
			wantName:  "organizations/acme/resourcegroups/foo",
			wantChild: true,
		},
		{
			name:      "resource relative to itself",
			resource:  "organizations/acme",
			ancestor:  "organizations/acme",
			wantName:  "",
			wantChild: true,
		},
		{
			name:      "ancestor is not a prefix",
			resource:  "organizations/acme/resourcegroups/foo",
			ancestor:  "organization/other",
			wantName:  "",
			wantChild: false,
		},
		{
			name:      "ancestor is longer than resource",
			resource:  "organizations/acme",
			ancestor:  "organizations/acme/resourcegroups/foo",
			wantName:  "",
			wantChild: false,
		},
		{
			name:      "ancestor is similar but not matching prefix",
			resource:  "organizations/acme123",
			ancestor:  "organizations/acme",
			wantName:  "",
			wantChild: false,
		},
		{
			name:      "empty resource with empty ancestor",
			resource:  "",
			ancestor:  "",
			wantName:  "",
			wantChild: true,
		},
		{
			name:      "empty resource with non-empty ancestor",
			resource:  "",
			ancestor:  "organizations/acme",
			wantName:  "",
			wantChild: false,
		},
		{
			name:      "single level resource with empty ancestor",
			resource:  "organizations/acme",
			ancestor:  "",
			wantName:  "organizations/acme",
			wantChild: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotChild := tt.resource.Relative(tt.ancestor)
			if gotName != tt.wantName {
				t.Errorf("ResourceName.Relative() name = %v, want %v", gotName, tt.wantName)
			}
			if gotChild != tt.wantChild {
				t.Errorf("ResourceName.Relative() isChild = %v, want %v", gotChild, tt.wantChild)
			}
		})
	}
}
