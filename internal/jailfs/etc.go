package jailfs

import (
	"os"
	"path/filepath"
)

// WriteEtcFiles writes configuration files needed in the jail's /etc directory.
func WriteEtcFiles(paths *Paths) error {
	resolvConfPath := filepath.Join(paths.Etc, "resolv.conf")

	// Do not overwrite existing resolv.conf
	exists, err := fileExists(resolvConfPath)
	if err != nil {
		return err
	}
	if !exists {
		return writeResolvConf(resolvConfPath)
	}
	return nil
}

// fileExists checks if a file exists at the given path.
// Returns (true, nil) if file exists, (false, nil) if file doesn't exist,
// and (false, error) if there was an error checking.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func writeResolvConf(path string) error {
	// Use the same address as podman with pasta
	content := "nameserver 169.254.1.1"
	return os.WriteFile(path, []byte(content), 0600)
}
