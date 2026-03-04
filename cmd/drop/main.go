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

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/wrr/drop/internal/cli"
	"github.com/wrr/drop/internal/command"
	"github.com/wrr/drop/internal/jailfs"
	"github.com/wrr/drop/internal/osutil"
	"github.com/wrr/drop/internal/updater"
)

var Version = "dev" // overridden by release binaries linker

func main() {
	var exitCode int
	var err error
	if len(os.Args) > 1 && os.Args[1] == "-child" {
		exitCode, err = childProcessEntry()
	} else {
		exitCode, err = parentProcessEntry()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	os.Exit(exitCode)
}

func parentProcessEntry() (int, error) {
	if os.Geteuid() == 0 {
		return 1, fmt.Errorf("drop should not be run as root")
	}

	homeDir, err := osutil.CurrentUserHomeDir()
	if err != nil {
		return 1, err
	}

	dropHome, err := jailfs.DropHome(homeDir)
	if err != nil {
		return 1, err
	}

	handlers := cli.Handlers{
		Init: func(envId string, noCwd bool) error {
			err := command.InitEnv(envId, noCwd, homeDir, dropHome)
			if err != nil {
				return fmt.Errorf("failed to init environment: %v", err)
			}
			return nil
		},
		Run: func(flags *cli.RunFlags) error {
			return command.RunParent(flags, homeDir, dropHome)
		},
		Ls: func() error {
			envs, err := jailfs.LsEnvs(dropHome)
			if err != nil {
				return fmt.Errorf("failed to list environments: %v", err)
			}
			for _, envId := range envs {
				fmt.Println(envId)
			}
			return nil
		},
		Rm: func(envId string) error {
			if err := jailfs.RmEnv(homeDir, dropHome, envId); err != nil {
				return fmt.Errorf("failed to remove environment '%s': %v", envId, err)
			}
			return nil
		},
		Update: func(checkOnly bool) error {
			if !checkOnly {
				return fmt.Errorf("automatic updating not yet available, you can check for updates with 'drop update --check'")
			}
			newVersion, err := updater.CheckForUpdate(Version)
			if err != nil {
				return fmt.Errorf("failed to check for updates: %v", err)
			}
			if newVersion != "" {
				fmt.Printf("Drop %s is available, your installed version is %s\n", newVersion, Version)
				fmt.Printf("Download: https://github.com/wrr/drop/releases/latest\n")
			} else {
				fmt.Printf("Drop is up to date (version %s)\n", Version)
			}
			return nil
		},
	}

	cmd := cli.Command(Version, handlers)
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			// Propagate child's exec exit code without printing any error message.
			return exitError.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func childProcessEntry() (int, error) {
	err := command.RunChild()
	if err != nil {
		return 1, err
	}
	return 0, nil
}
