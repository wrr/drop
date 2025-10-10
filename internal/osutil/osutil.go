package osutil

import (
	"fmt"
	"os"
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
