// Copyright 2025 Jan Wrobel <jan@mixedbit.org>
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

package jailfs

import (
	"slices"
	"testing"

	"github.com/wrr/drop/internal/config"
)

func TestResolveHomeDirInMounts(t *testing.T) {
	mounts := []config.Mount{
		{Source: "~/foo", Target: "~/bar", RW: true},
		{Source: "/etc", Target: "~/baz", RW: false},
	}
	result := resolveHomeDirInMounts(mounts, "/home/alice")
	expected := []config.Mount{
		{Source: "/home/alice/foo", Target: "/home/alice/bar", RW: true},
		{Source: "/etc", Target: "/home/alice/baz", RW: false},
	}
	if !slices.Equal(result, expected) {
		t.Errorf("Expected %+v, got %+v", expected, result)
	}
}

func TestResolveHomeDirInPaths(t *testing.T) {
	paths := []string{
		"~/foo", "~/bar", "/etc", "~/baz",
	}
	result := resolveHomeDirInPaths(paths, "/home/alice")
	expected := []string{
		"/home/alice/foo", "/home/alice/bar", "/etc", "/home/alice/baz",
	}
	if !slices.Equal(result, expected) {
		t.Errorf("Expected %+v, got %+v", expected, result)
	}
}

func TestPrependCwdToMounts(t *testing.T) {
	mounts := []config.Mount{
		{Source: ".", Target: ".", RW: true},
		{Source: ".git", Target: ".git2", RW: false},
	}
	result := prependCwdToMounts(mounts, "/home/alice/project")
	expected := []config.Mount{
		{Source: "/home/alice/project", Target: "/home/alice/project", RW: true},
		{Source: "/home/alice/project/.git", Target: "/home/alice/project/.git2", RW: false},
	}
	if !slices.Equal(result, expected) {
		t.Errorf("Expected %+v, got %+v", expected, result)
	}
}

func TestPrependCwdToPaths(t *testing.T) {
	paths := []string{
		".", ".git", "./foo/bar", "baz",
	}
	result := prependCwdToPaths(paths, "/tmp/x/")
	expected := []string{
		"/tmp/x", "/tmp/x/.git", "/tmp/x/foo/bar", "/tmp/x/baz",
	}
	if !slices.Equal(result, expected) {
		t.Errorf("Expected %+v, got %+v", expected, result)
	}
}
