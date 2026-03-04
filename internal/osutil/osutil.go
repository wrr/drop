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
)

// MkdirAll is a simple wrapper over os.MkdirAll. It always uses
// permissions 0700 and returns verbose error which can be propagated
// up without adding an aditional context.
func MkdirAll(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", path, err)
	}
	return nil
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

// IsDebianBased returns true if the system is Debian-based by checking
// for the presence of /etc/debian_version file.
func IsDebianBased() bool {
	return Exists("/etc/debian_version")
}

// TildeToHomeDir replaces tilde in a path with the given
// homeDir path. Does not handle tildes followed by username (~bob).
func TildeToHomeDir(path string, homeDir string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, strings.TrimPrefix(path, "~"))
	}
	return path
}

func IsRootOrHomeSubPath(path string) bool {
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~/")
}

// isSubDirOrSame returns true if child is a sub directory of the parent.
func IsSubDir(parent, child string) bool {
	parent = cleanDir(parent)
	child = cleanDir(child)
	return child != parent && strings.HasPrefix(child, parent)
}

// IsSubDirOrSame returns true if child is a sub directory of the parent
// or if they are the same directory.
func IsSubDirOrSame(parent, child string) bool {
	parent = cleanDir(parent)
	child = cleanDir(child)
	return strings.HasPrefix(child, parent)
}

func cleanDir(dir string) string {
	sep := string(filepath.Separator)
	dir = filepath.Clean(dir)
	if !strings.HasSuffix(dir, sep) {
		dir += sep
	}
	return dir
}

// ValidateRootOrHomeSubPath validates that a path is a subpath of root or
// of a ~/, and is normalized.
func ValidateRootOrHomeSubPath(path string) error {
	if !IsRootOrHomeSubPath(path) {
		return fmt.Errorf("path must start with / or ~/")
	}
	if path == "/" {
		return fmt.Errorf("path cannot point to the whole root directory")
	}
	if path == "~/" {
		return fmt.Errorf("path cannot point to the whole home directory")
	}

	// Remove ~ for validation with Clean()
	path = strings.TrimPrefix(path, "~")

	// filepath.Clean() removes trailing / from all paths except /.  We
	// allow for trailing /, so we remove it before validation.
	path = strings.TrimSuffix(path, "/")

	if path != filepath.Clean(path) {
		return fmt.Errorf("path is not normalized")
	}
	return nil
}

// ValidateRelPath validates that path is a relative path and is normalized.
func ValidateRelPath(path string) error {
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~/") {
		return fmt.Errorf("path must be relative, cannot start with / or ~/")
	}
	if path == "." || path == "./" {
		return nil
	}
	// Allow for trailing / and leading ./
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimPrefix(path, "./")

	if path != filepath.Clean(path) || strings.HasPrefix(path, "..") {
		return fmt.Errorf("path is not normalized")
	}
	return nil
}

// CurrentUserHomeDir returns the home directory of the current user.
func CurrentUserHomeDir() (string, error) {
	if home := os.Getenv("HOME"); home != "" {
		return home, nil
	}
	return "", fmt.Errorf("failed to determine the current user home directory; HOME environment variable is not set")
}
