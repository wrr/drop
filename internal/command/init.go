// Copyright 2025-2026 Jan Wrobel <jan@mixedbit.org>
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

// 'drop init ...' command handling

package command

import (
	"fmt"
	"os"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/jailfs"
	"github.com/wrr/drop/internal/osutil"
)

// InitEnv creates a new drop environment ('drop init envid' command).
//
// It creates the environment directory in dropHome, and environment
// specific config file which extends base config file in the drop
// config directory.
//
// If base config file is missing (first run of 'drop init'), the
// function creates the default base.toml.
func InitEnv(envId string, noCwd bool, homeDir, dropHome string) error {
	success := false

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current working directory: %v", err)
	}

	err = jailfs.CreateEnvDir(dropHome, envId)
	if err != nil {
		return err
	}
	defer func() {
		if !success {
			// Remove dir created by CreateEnvDir, best effort, ignore errors
			jailfs.RmEnv(homeDir, dropHome, envId)
		}
	}()

	baseConfigPath := jailfs.BaseConfigPath(homeDir)
	if !osutil.Exists(baseConfigPath) {
		if err := config.WriteDefault(baseConfigPath, homeDir); err != nil {
			return fmt.Errorf("write base config to %v: %v", baseConfigPath, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote base Drop config to %s\n", baseConfigPath)
	}

	envConfigPath := jailfs.EnvConfigPath(homeDir, envId)
	if !osutil.Exists(envConfigPath) {
		var mounts []config.DefaultMount
		// Add cwd to configured mounts, but only if cwd is not the home
		// directory or a parent of it to avoid exposing the whole home
		// directory.
		if !noCwd && !osutil.IsSubDirOrSame(cwd, homeDir) {
			mounts = []config.DefaultMount{
				{Entry: cwd + "::rw", Comment: "Allow read-write"},
				{Entry: cwd + "/.git", Comment: "Allow read-only, block changing git config or adding hooks"},
			}
		}

		err = config.WriteDefaultForEnv(envConfigPath, mounts, homeDir)
		if err != nil {
			return fmt.Errorf("write env config to %v: %v", envConfigPath, err)
		}
	}
	fmt.Fprintf(os.Stderr, "Drop environment created with config at %s\n", envConfigPath)
	success = true
	return nil
}
