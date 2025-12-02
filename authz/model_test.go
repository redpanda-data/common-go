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
			resource: "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
			want:     "qux",
		},
		{
			name:     "short path",
			resource: "organization/acme",
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
			resource: "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
			want:     "mcpserver",
		},
		{
			name:     "short path",
			resource: "organization/acme",
			want:     "organization",
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
			resource: "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
			want:     "organization/acme/resourcegroup/foo/dataplane/bar",
		},
		{
			name:     "short path",
			resource: "organization/acme/resourcegroup/foo",
			want:     "organization/acme",
		},
		{
			name:     "minimal path",
			resource: "organization/acme",
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
			resource:     "organization/acme/resourcegroup/foo/dataplane/bar",
			resourceType: "mcpserver",
			resourceName: "qux",
			want:         "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
		},
		{
			name:         "add child to short path",
			resource:     "organization/acme",
			resourceType: "resourcegroup",
			resourceName: "foo",
			want:         "organization/acme/resourcegroup/foo",
		},
		{
			name:         "add child to empty resource",
			resource:     "",
			resourceType: "organization",
			resourceName: "acme",
			want:         "organization/acme",
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
			resource:  "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
			ancestor:  "organization/acme/resourcegroup/foo",
			wantName:  "dataplane/bar/mcpserver/qux",
			wantChild: true,
		},
		{
			name:      "deep resource with top-level ancestor",
			resource:  "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
			ancestor:  "organization/acme",
			wantName:  "resourcegroup/foo/dataplane/bar/mcpserver/qux",
			wantChild: true,
		},
		{
			name:      "resource with immediate parent",
			resource:  "organization/acme/resourcegroup/foo/dataplane/bar",
			ancestor:  "organization/acme/resourcegroup/foo",
			wantName:  "dataplane/bar",
			wantChild: true,
		},
		{
			name:      "resource relative to root (empty ancestor)",
			resource:  "organization/acme/resourcegroup/foo",
			ancestor:  "",
			wantName:  "organization/acme/resourcegroup/foo",
			wantChild: true,
		},
		{
			name:      "resource relative to itself",
			resource:  "organization/acme",
			ancestor:  "organization/acme",
			wantName:  "",
			wantChild: true,
		},
		{
			name:      "ancestor is not a prefix",
			resource:  "organization/acme/resourcegroup/foo",
			ancestor:  "organization/other",
			wantName:  "",
			wantChild: false,
		},
		{
			name:      "ancestor is longer than resource",
			resource:  "organization/acme",
			ancestor:  "organization/acme/resourcegroup/foo",
			wantName:  "",
			wantChild: false,
		},
		{
			name:      "ancestor is similar but not matching prefix",
			resource:  "organization/acme123",
			ancestor:  "organization/acme",
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
			ancestor:  "organization/acme",
			wantName:  "",
			wantChild: false,
		},
		{
			name:      "single level resource with empty ancestor",
			resource:  "organization/acme",
			ancestor:  "",
			wantName:  "organization/acme",
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
