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
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestIsEnvIdValid(t *testing.T) {
	tests := []struct {
		name  string
		envId string
		want  bool
	}{
		{
			name:  "simple alphanumeric",
			envId: "project123",
			want:  true,
		},
		{
			name:  "with dash",
			envId: "project-foo",
			want:  true,
		},
		{
			name:  "with underscore",
			envId: "project_foo",
			want:  true,
		},
		{
			name:  "with dot",
			envId: "project.foo",
			want:  true,
		},
		{
			name:  "mixed valid characters",
			envId: "Project_123-test.v2",
			want:  true,
		},
		{
			name:  "single character",
			envId: "a",
			want:  true,
		},
		{
			name:  "starts with dash",
			envId: "-project",
			want:  false,
		},
		{
			name:  "starts with dot",
			envId: ".project",
			want:  false,
		},
		{
			name:  "with slash",
			envId: "project/foo",
			want:  false,
		},
		{
			name:  "with space",
			envId: "project foo",
			want:  false,
		},
		{
			name:  "with special char",
			envId: "project@foo",
			want:  false,
		},
		{
			name:  "empty",
			envId: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsEnvIdValid(tt.envId)
			if got != tt.want {
				t.Errorf("IsEnvIdValid(%q) = %v, want %v", tt.envId, got, tt.want)
			}
		})
	}
}

func TestPathToEnvId(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		// Root directory special case
		{
			name: "root directory",
			path: "/",
			want: "root",
		},
		{
			name: "single level",
			path: "/tmp",
			want: "tmp",
		},
		{
			name: "nested path",
			path: "/home/alice/projects/foo",
			want: "home-alice-projects-foo",
		},
		{
			name: "path with dots",
			path: "/home/alice/project.v1.2",
			want: "home-alice-project.v1.2",
		},
		{
			name: "top dir with dot",
			path: "/.hidden",
			want: "hidden",
		},
		{
			name: "path with spaces",
			path: "/home/alice/my project",
			want: "home-alice-my_project",
		},
		{
			name: "path with special chars",
			path: "/home/alice/project@work",
			want: "home-alice-project_work",
		},
		{
			name: "path with parentheses",
			path: "/home/alice/project(v2)",
			want: "home-alice-project_v2_",
		},
		{
			name: "path with underscores",
			path: "/home/alice/my_project",
			want: "home-alice-my_project",
		},
		{
			name: "path with numbers",
			path: "/home/alice/project123",
			want: "home-alice-project123",
		},
		// Edge cases, should not be triggered with input path being CWD.
		{
			name: "empty string",
			path: "",
			want: "root",
		},
		{
			name: "path with only slashes",
			path: "///",
			want: "root",
		},
		{
			name: "path with only dots and slashes",
			path: "/./.././/",
			want: "root",
		},
		{
			name: "only dot",
			path: ".",
			want: "root",
		},
		{
			name: "relative path",
			path: "relative/path",
			want: "relative-path",
		},
		{
			name: "path ending with slash",
			path: "/home/alice/",
			want: "home-alice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathToEnvId(tt.path)
			if got != tt.want {
				t.Errorf("pathToEnvId(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestDefaultConfigPath(t *testing.T) {
	origDropHome := os.Getenv("DROP_HOME")
	defer os.Setenv("DROP_HOME", origDropHome)
	os.Unsetenv("DROP_HOME")

	origXdgConfigHome := os.Getenv("XDG_CONFIG_HOME")
	defer os.Setenv("XDG_CONFIG_HOME", origXdgConfigHome)
	os.Unsetenv("XDG_CONFIG_HOME")

	home := "/home/alice/"
	result := BaseConfigPath(home)
	expected := "/home/alice/.config/drop/base.toml"
	if result != expected {
		t.Errorf("DefaultConfigPath(%q) = %q", home, result)
	}

	os.Setenv("XDG_CONFIG_HOME", "/home/alice/configs")
	result = BaseConfigPath(home)
	expected = "/home/alice/configs/drop/base.toml"
	if result != expected {
		t.Errorf("DefaultConfigPath(%q) = %q", home, result)
	}

	expected = "/home/alice/drop-home/config/base.toml"
	os.Setenv("DROP_HOME", "/home/alice/drop-home/")
	result = BaseConfigPath(home)
	if result != expected {
		t.Errorf("DefaultConfigPath(%q) = %q", home, result)
	}

}

func TestDropHome(t *testing.T) {
	homeDir := "/home/alice"

	tests := []struct {
		name        string
		dropHome    string
		xdgDataHome string
		want        string
		wantError   string
	}{
		{
			name: "envs not set",
			want: "/home/alice/.local/share/drop",
		},
		{
			name:     "absolute DROP_HOME",
			dropHome: "/var/drop-data",
			want:     "/var/drop-data",
		},
		{
			name:     "tilde DROP_HOME",
			dropHome: "~/.my-drop",
			want:     "/home/alice/.my-drop",
		},
		{
			name:        "absolute XDG_DATA_HOME",
			xdgDataHome: "/home/alice/data",
			want:        "/home/alice/data/drop",
		},
		{
			name:      "invalid DROP_HOME: relative path",
			dropHome:  "relative/path",
			wantError: "path must start with / or ~/",
		},
		{
			name:      "invalid DROP_HOME: not normalized",
			dropHome:  "/var/../etc/drop",
			wantError: "path is not normalized",
		},
		{
			name:      "invalid DROP_HOME whole root",
			dropHome:  "/",
			wantError: "path cannot point to the whole root directory",
		},
		{
			name:      "invalid DROP_HOME: whole home",
			dropHome:  "~/",
			wantError: "path cannot point to the whole home directory",
		},
		{
			name:        "invalid XDG_DATA_HOME: relative path",
			xdgDataHome: "ralative/path",
			wantError:   "path must start with / or ~/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origDropHome := os.Getenv("DROP_HOME")
			defer os.Setenv("DROP_HOME", origDropHome)
			origXdgDataHome := os.Getenv("XDG_DATA_HOME")
			defer os.Setenv("XDG_DATA_HOME", origXdgDataHome)

			if tt.dropHome != "" {
				os.Setenv("DROP_HOME", tt.dropHome)
			} else {
				os.Unsetenv("DROP_HOME")
			}

			if tt.xdgDataHome != "" {
				os.Setenv("XDG_DATA_HOME", tt.xdgDataHome)
			} else {
				os.Unsetenv("XDG_DATA_HOME")
			}

			got, err := DropHome(homeDir)
			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantError)
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("expected error containing %q, got %q", tt.wantError, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("DropHome(%q) = %q, want %q", homeDir, got, tt.want)
			}
		})
	}
}

func TestTmpDirExistsWithRightPerms(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "foo")
	if tmpDirExistsWithRightPerms(path) {
		t.Fatalf("expected %q to be missing", path)
	}
	if err := os.Mkdir(path, 0x755); err != nil {
		t.Fatalf("mkdir failed %v", err)
	}
	if tmpDirExistsWithRightPerms(path) {
		t.Fatalf("expected %q to have invalid perms", path)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	if !tmpDirExistsWithRightPerms(path) {
		t.Fatalf("expected %q to be accepted", path)
	}
}

func TestCreateTmpParentDir(t *testing.T) {
	tmpDir := t.TempDir()

	origTmpDir := os.Getenv("TMPDIR")
	os.Setenv("TMPDIR", tmpDir)
	defer os.Setenv("TMPDIR", origTmpDir)

	path, err := createTmpParentDir("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(tmpDir, "drop-alice")
	if path != expected {
		t.Fatalf("createTmpParentDir returned %q, want %q", path, expected)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if !stat.IsDir() {
		t.Fatal("created path is not a directory")
	}
	if stat.Mode().Perm() != 0700 {
		t.Fatalf("directory perms %o, want 0700", stat.Mode().Perm())
	}

	if sysStats, ok := stat.Sys().(*syscall.Stat_t); ok {
		uid := os.Getuid()
		if int(sysStats.Uid) != uid {
			t.Fatalf("directory owned by %d, want %d", sysStats.Uid, uid)
		}
	}

	// Should reuse the same path
	path2, err := createTmpParentDir("alice")
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if path2 != expected {
		t.Fatalf("expected path to be reused, got %q want %q", path, expected)
	}

	// Change permission so the original parent dir is no longer usable
	// as the parent dir.
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	path3, err := createTmpParentDir("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path3 == expected {
		t.Fatal("expected fallback to random suffix, got same path")
	}

	expectedPrefix := "drop-alice-"
	dirName := filepath.Base(path3)
	if !strings.HasPrefix(dirName, expectedPrefix) {
		t.Fatalf("path %q doesn't have prefix %q", dirName, expectedPrefix)
	}

	stat, err = os.Stat(path3)
	if err != nil {
		t.Fatalf("fallback directory not created: %v", err)
	}
	if stat.Mode().Perm() != 0700 {
		t.Fatalf("fallback dir perms are %o, want 0700", stat.Mode().Perm())
	}
}
