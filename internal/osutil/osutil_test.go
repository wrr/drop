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
	"path/filepath"
	"strings"
	"syscall"
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

func TestHomeDirToTilde(t *testing.T) {
	homeDir := "/home/alice"
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "subdir of home",
			path: "/home/alice/project",
			want: "~/project",
		},
		{
			name: "nested subdir of home",
			path: "/home/alice/code/drop",
			want: "~/code/drop",
		},
		{
			name: "equal to home",
			path: "/home/alice",
			want: "~",
		},
		{
			name: "equal to home with trailing slash",
			path: "/home/alice/",
			want: "~",
		},
		{
			name: "outside home",
			path: "/opt/code",
			want: "/opt/code",
		},
		{
			name: "outside home, same prefix",
			path: "/home/alice2/project",
			want: "/home/alice2/project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HomeDirToTilde(tt.path, homeDir)
			if got != tt.want {
				t.Errorf("HomeDirToTilde(%q, %q) = %q, want %q", tt.path, homeDir, got, tt.want)
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

func TestCreateEmptyFile(t *testing.T) {
	// Clear the umask so the requested permissions are applied verbatim.
	oldMask := syscall.Umask(0)
	defer syscall.Umask(oldMask)

	dir := t.TempDir()
	path := filepath.Join(dir, "empty")

	if err := CreateEmptyFile(path, 0600); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat created file: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("created file should be empty, got size %d", info.Size())
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("created file permissions = %o, want %o", got, 0600)
	}

	// Creating the same file again must fail as O_EXCL is used.
	err = CreateEmptyFile(path, 0600)
	if terr := checkError("create empty file", err); terr != nil {
		t.Fatal(terr)
	}
}

func TestCreateEmptyFilePermissions(t *testing.T) {
	oldMask := syscall.Umask(0)
	defer syscall.Umask(oldMask)

	dir := t.TempDir()
	path := filepath.Join(dir, "perm")

	if err := CreateEmptyFile(path, 0647); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat created file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0647 {
		t.Errorf("created file permissions = %o, want %o", got, 0647)
	}
}

func TestCanRead(t *testing.T) {
	dir := t.TempDir()

	testFile := filepath.Join(dir, "file")
	if err := os.WriteFile(testFile, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	testDir := filepath.Join(dir, "dir")
	if err := os.Mkdir(testDir, 0700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path string
		perm os.FileMode
		want bool
	}{
		{path: testFile, perm: 0600, want: true},
		{path: testFile, perm: 0400, want: true},
		{path: testFile, perm: 0000, want: false},
		{path: testFile, perm: 0200, want: false},
		{path: testFile, perm: 0100, want: false},
		{path: testFile, perm: 0040, want: false},
		{path: testFile, perm: 0004, want: false},
		{path: testDir, perm: 0700, want: true},
		{path: testDir, perm: 0400, want: true},
		{path: testDir, perm: 0000, want: false},
		{path: testDir, perm: 0100, want: false},
		{path: testDir, perm: 0040, want: false},
	}

	for _, tt := range tests {
		if err := os.Chmod(tt.path, tt.perm); err != nil {
			t.Fatal(err)
		}
		if got := CanRead(tt.path); got != tt.want {
			t.Errorf("CanRead(%s, perm %o) = %v, want %v", tt.path, tt.perm, got, tt.want)
		}
	}

	if CanRead(filepath.Join(dir, "does-not-exist")) {
		t.Error("CanRead of a missing path should be false")
	}
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

func TestIsSubDir(t *testing.T) {
	tests := []struct {
		name           string
		parent         string
		child          string
		isSubDir       bool
		isSubDirOrSame bool
	}{
		{
			name:           "direct parent",
			parent:         "/home/alice",
			child:          "/home/alice/documents",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "nested parent",
			parent:         "/home",
			child:          "/home/alice/documents/file.txt",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "not parent - sibling",
			parent:         "/home/alice",
			child:          "/home/other",
			isSubDir:       false,
			isSubDirOrSame: false,
		},
		{
			name:           "not parent - completely different",
			parent:         "/home/alice",
			child:          "/var/log",
			isSubDir:       false,
			isSubDirOrSame: false,
		},
		{
			name:           "same directory",
			parent:         "/home/alice",
			child:          "/home/alice",
			isSubDir:       false,
			isSubDirOrSame: true,
		},
		{
			name:           "parent with trailing slash",
			parent:         "/home/alice/",
			child:          "/home/alice/documents",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "child with relative components",
			parent:         "/home/alice",
			child:          "/home/alice/..",
			isSubDir:       false,
			isSubDirOrSame: false,
		},
		{
			name:           "child with relative components 2",
			parent:         "/home/alice",
			child:          "/home/alice/../../home/alice",
			isSubDir:       false,
			isSubDirOrSame: true,
		},
		{
			name:           "parent with relative components",
			parent:         "/home/./alice/..",
			child:          "/home/documents",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "root as parent",
			parent:         "/",
			child:          "/home/alice",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "root as parent and child",
			parent:         "/",
			child:          "/",
			isSubDir:       false,
			isSubDirOrSame: true,
		},
		{
			name:           "substring but not parent",
			parent:         "/home/use",
			child:          "/home/alice",
			isSubDir:       false,
			isSubDirOrSame: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsSubDir(tt.parent, tt.child)
			if result != tt.isSubDir {
				t.Errorf("isSubDir(%q, %q) = %v, want %v", tt.parent, tt.child, result, tt.isSubDir)
			}
			result = IsSubDirOrSame(tt.parent, tt.child)
			if result != tt.isSubDirOrSame {
				t.Errorf("isSubDirOrSame(%q, %q) = %v, want %v", tt.parent, tt.child, result, tt.isSubDirOrSame)
			}
		})
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
	if terr := checkError("HOME environment variable is not set", err); terr != nil {
		t.Fatal(terr)
	}
}
