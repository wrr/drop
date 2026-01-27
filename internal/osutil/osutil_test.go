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

package osutil

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestIsDebian(t *testing.T) {
	// This test just ensures the function doesn't panic and returns a boolean
	// The actual result depends on the host system
	result := IsDebianBased()

	// Should return either true or false (boolean type check)
	if result != true && result != false {
		t.Error("IsDebian should return a boolean value")
	}
}

func TestTildeToHomeDir(t *testing.T) {
	homeDir := "/home/alice"
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "tilde path with single file",
			path: "~/.bashrc",
			want: "/home/alice/.bashrc",
		},
		{
			name: "tilde path with trailing slash",
			path: "~/Documents/",
			want: "/home/alice/Documents",
		},
		{
			name: "to tilde - no change",
			path: "/usr/local/bin",
			want: "/usr/local/bin",
		},
		{
			name: "tilde alone",
			path: "~",
			want: homeDir,
		},
		{
			name: "tilde with different user",
			path: "~otheruser/file",
			want: "~otheruser/file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TildeToHomeDir(tt.path, homeDir)
			if got != tt.want {
				t.Errorf("TildeToHomeDir(%q, %q) = %q, want %q", tt.path, homeDir, got, tt.want)
			}
		})
	}
}

func checkError(wantErr string, err error) error {
	if wantErr == "" {
		if err != nil {
			return fmt.Errorf("unexpected error: %v", err)
		}
		return nil
	}
	if err == nil {
		return fmt.Errorf("expected error containing %q, got nil", wantErr)
	}
	if !strings.Contains(err.Error(), wantErr) {
		return fmt.Errorf("expected error containing %q, got %q", wantErr, err.Error())
	}
	return nil
}

func TestIsRootOrHomeSubPath(t *testing.T) {
	tests := []struct {
		path   string
		result bool
	}{
		{
			path:   "/",
			result: true,
		},
		{
			path:   "/tmp",
			result: true,
		},
		{
			path:   "~/",
			result: true,
		},
		{
			path:   "~/bin",
			result: true,
		},
		{
			path:   "./bin",
			result: false,
		},
		{
			path:   ".",
			result: false,
		},
		{
			path:   "bin",
			result: false,
		},
	}
	for _, tt := range tests {
		if IsRootOrHomeSubPath(tt.path) != tt.result {
			t.Errorf("Invalid result for %s", tt.path)
		}
	}
}

func TestValidateRootOrHomeSubPath(t *testing.T) {
	tests := []struct {
		error string
		paths []string
	}{
		{
			error: "",
			paths: []string{"/usr/local", "/usr/local/", "~/tmp/docs", "~/tmp/docs/", "~/.bashrc"},
		},
		{
			error: "path must start with / or ~/",
			paths: []string{"", "docs/file.txt", "~user", "~"},
		},
		{
			error: "path is not normalized",
			paths: []string{"/home/../etc/passwd", "~/../secrets", "/home/./user", "/home/user/.", "/home//user"},
		},
		{
			error: "path cannot point to the whole root directory",
			paths: []string{"/"},
		},
		{
			error: "path cannot point to the whole home directory",
			paths: []string{"~/"},
		},
	}

	for _, tt := range tests {
		for _, path := range tt.paths {
			t.Run(fmt.Sprintf("path=%q", path), func(t *testing.T) {
				err := ValidateRootOrHomeSubPath(path)
				if terr := checkError(tt.error, err); terr != nil {
					t.Fatal(terr)
				}
			})
		}
	}
}

func TestValidateRelPath(t *testing.T) {
	tests := []struct {
		error string
		paths []string
	}{
		{
			error: "",
			paths: []string{"local", "local/", "local/bin", "./local", ".", "./"},
		},
		{
			error: "path must be relative",
			paths: []string{"/local", "~/local"},
		},
		{
			error: "path is not normalized",
			paths: []string{"", "local/../bin", "local/./bin", "local/bin/.", "local//bin", "../.git"},
		},
	}

	for _, tt := range tests {
		for _, path := range tt.paths {
			t.Run(fmt.Sprintf("path=%q", path), func(t *testing.T) {
				err := ValidateRelPath(path)
				if terr := checkError(tt.error, err); terr != nil {
					t.Fatal(terr)
				}

			})
		}
	}
}

func TestCurrentUserHomeDir(t *testing.T) {
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	os.Setenv("HOME", "/home/alice")
	home, err := CurrentUserHomeDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if home != "/home/alice" {
		t.Fatalf("Invalid user home dir: %q", home)
	}
	os.Unsetenv("HOME")
	_, err = CurrentUserHomeDir()
	if terr := checkError("failed to determine the current user home directory", err); terr != nil {
		t.Fatal(terr)
	}
}
