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

// ValidateRootOrHomeSubPath validates that a path is a subpath of root or
// of a ~/, and is normalized.
func ValidateRootOrHomeSubPath(path string) error {
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "~/") {
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
