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
