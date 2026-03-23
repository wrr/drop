// Copyright 2026 Jan Wrobel <jan@mixedbit.org>
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

// Package cli is responsible for Drop command line arguments parsing
// and processing.

package cli

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/jailfs"
)

// RunFlags contains parsed command line flags for the 'drop run'
// command.
type RunFlags struct {
	EnvId       string
	ConfigPath  string
	NetworkMode string

	Mounts []string

	BeRoot            bool
	TcpPublishedPorts []string
	TcpHostPorts      []string
	UdpPublishedPorts []string
	UdpHostPorts      []string

	// Remaining command line arguments (the command to execute)
	Args []string
}

// Handlers contains callback functions for each command.
type Handlers struct {
	Init   func(envId string, noCwd bool) error
	Run    func(flags *RunFlags) error
	Ls     func() error
	Rm     func(envId string) error
	Update func(checkOnly bool) error
}

// Command creates the urfave/cli command with all commands and flags configured.
func Command(version string, handlers Handlers) *cli.Command {
	defaultEnvId, _ := jailfs.CwdToEnvId()
	var flags RunFlags
	return &cli.Command{
		Name:      "drop",
		Usage:     "Run programs in sandboxed environments",
		UsageText: "drop <command> [options]",
		Version:   version,
		ExitErrHandler: func(_ context.Context, _ *cli.Command, err error) {
			// blank to avoid the call to os.Exit which drop makes explicitly in main
		},
		Commands: []*cli.Command{
			{
				Name:        "init",
				Usage:       "Create a new Drop environment",
				ArgsUsage:   "[env-id]",
				Description: "If env-id is not given, it is derived from the current working directory.",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "no-cwd",
						Aliases: []string{"nc"},
						Usage:   "Do not configure the environment to mount the current working directory",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					var envId string
					if cmd.NArg() > 1 {
						return cli.Exit("usage: drop init [env-id]", 1)
					}
					if cmd.NArg() == 0 {
						envId = defaultEnvId
					} else {
						envId = cmd.Args().First()
					}
					return handlers.Init(envId, cmd.Bool("no-cwd"))
				},
			},
			{
				Name:  "run",
				Usage: "Run a command in a Drop environment",
				Description: `If -e is not given, env-id is derived from the current working directory.
If -c is not given, the config file is ENV_ID.toml in the Drop config directory.
The -m, -t, -T, -u, -U options are appended to options from the TOML config file.`,
				ArgsUsage:    "[command...]",
				StopOnNthArg: intPtr(1),
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:        "env",
						Aliases:     []string{"e"},
						Usage:       "Environment ID",
						Value:       defaultEnvId,
						Destination: &flags.EnvId,
					},
					&cli.StringFlag{
						Name:        "config",
						Aliases:     []string{"c"},
						Usage:       "Path to TOML config file",
						Destination: &flags.ConfigPath,
					},
					&cli.BoolFlag{
						Name:        "root",
						Aliases:     []string{"r"},
						Usage:       "Be root (uid 0) in the sandbox (doesn't grant any additional privileges to the sandboxed processes).",
						Destination: &flags.BeRoot,
					},
					&cli.StringSliceFlag{
						Name:        "mount",
						Aliases:     []string{"m"},
						Usage:       "Add a mount (format: source[:target][:rw])",
						Destination: &flags.Mounts,
					},
					&cli.StringFlag{
						Name:        "net",
						Aliases:     []string{"n"},
						Usage:       "Network mode: off or isolated",
						Destination: &flags.NetworkMode,
					},
					&cli.StringSliceFlag{
						Name:        "tcp-publish",
						Aliases:     []string{"t"},
						Usage:       "Publish a TCP port from the sandbox (format: [hostIP/]hostPort[:sandboxPort])",
						Destination: &flags.TcpPublishedPorts,
					},
					&cli.StringSliceFlag{
						Name:        "tcp-host",
						Aliases:     []string{"T"},
						Usage:       "Make a TCP port from the host available in the sandbox (format: hostPort[:sandboxPort])",
						Destination: &flags.TcpHostPorts,
					},
					&cli.StringSliceFlag{
						Name:        "udp-publish",
						Aliases:     []string{"u"},
						Usage:       "Publish a UDP port from the sandbox (format: [hostIP/]hostPort[:sandboxPort])",
						Destination: &flags.UdpPublishedPorts,
					},
					&cli.StringSliceFlag{
						Name:        "udp-host",
						Aliases:     []string{"U"},
						Usage:       "Make a UDP port from the host available in the sandbox (format: hostPort[:sandboxPort])",
						Destination: &flags.UdpHostPorts,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					flags.Args = cmd.Args().Slice()

					if flags.EnvId == "" {
						return fmt.Errorf("could not determine environment ID from current directory")
					}
					if !jailfs.IsEnvIdValid(flags.EnvId) {
						return fmt.Errorf("invalid character in env ID")
					}

					return handlers.Run(&flags)
				},
			},
			{
				Name:      "ls",
				Usage:     "List available Drop environments",
				ArgsUsage: " ",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() != 0 {
						return cli.Exit("usage: drop ls", 1)
					}
					return handlers.Ls()
				},
			},
			{
				Name:      "rm",
				Usage:     "Remove a Drop environment",
				ArgsUsage: "<env-id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.NArg() != 1 {
						return cli.Exit("usage: drop rm <env-id>", 1)
					}
					envId := cmd.Args().First()
					return handlers.Rm(envId)
				},
			},
			{
				Name:        "update",
				Usage:       "Check if a new version of Drop is available",
				ArgsUsage:   " ",
				Description: "The --check flag is currently required. Automatic updating is not yet available.",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "check",
						Aliases: []string{"c"},
						Usage:   "Check for updates",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return handlers.Update(cmd.Bool("check"))
				},
			},
		},
	}
}

// FlagsToConfig modifies cfg from a TOML file with values passed via
// command line flags. The function validates config after the
// modification.
func FlagsToConfig(cfg *config.Config, flags *RunFlags) error {
	for _, m := range flags.Mounts {
		mount, err := config.ParseMountCompact(m)
		if err != nil {
			return fmt.Errorf("command line --mount flag: %v", err)
		}
		cfg.Mounts = append(cfg.Mounts, *mount)
	}

	if flags.NetworkMode != "" {
		cfg.Net.Mode = flags.NetworkMode
	}
	if len(flags.TcpPublishedPorts) > 0 {
		p, err := parsePublishPortFlags(flags.TcpPublishedPorts)
		if err != nil {
			return fmt.Errorf("command line --tcp-publish flag: %v", err)
		}
		cfg.Net.TCPPublishedPorts = append(cfg.Net.TCPPublishedPorts, p...)
	}
	if len(flags.TcpHostPorts) > 0 {
		p, err := parseHostPortFlags(flags.TcpHostPorts)
		if err != nil {
			return fmt.Errorf("command line --tcp-host flag: %v", err)
		}
		cfg.Net.TCPHostPorts = append(cfg.Net.TCPHostPorts, p...)
	}
	if len(flags.UdpPublishedPorts) > 0 {
		p, err := parsePublishPortFlags(flags.UdpPublishedPorts)
		if err != nil {
			return fmt.Errorf("command line --udp-publish flag: %v", err)
		}
		cfg.Net.UDPPublishedPorts = append(cfg.Net.UDPPublishedPorts, p...)
	}
	if len(flags.UdpHostPorts) > 0 {
		p, err := parseHostPortFlags(flags.UdpHostPorts)
		if err != nil {
			return fmt.Errorf("command line --udp-host flag: %v", err)
		}
		cfg.Net.UDPHostPorts = append(cfg.Net.UDPHostPorts, p...)
	}
	// Validate config again, all errors detected should be related to
	// entries modified by this function, because cfg read from a file
	// and passed to this function was already validated during reading.
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("command line flags: %v", err)
	}
	return nil
}

func parsePublishPortFlags(flags []string) ([]config.PublishedPort, error) {
	var result []config.PublishedPort
	for _, spec := range flags {
		p, err := config.ParsePublishedPort(spec)
		if err != nil {
			return nil, err
		}
		result = append(result, *p)
	}
	return result, nil
}

func parseHostPortFlags(flags []string) ([]config.HostPort, error) {
	var result []config.HostPort
	for _, spec := range flags {
		p, err := config.ParseHostPort(spec)
		if err != nil {
			return nil, err
		}
		result = append(result, *p)
	}
	return result, nil
}

func intPtr(i int) *int {
	return &i
}
