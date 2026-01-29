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
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/jailfs"
)

// stringSlice implements flag.Value interface for repeated string flags
type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type Flags struct {
	Version     bool
	EnvId       string
	ConfigPath  string
	NetworkMode string

	Ls bool
	Rm string

	NoCwd  bool
	Mounts []string

	BeRoot            bool
	TcpPublishedPorts []string
	TcpHostPorts      []string
	UdpPublishedPorts []string
	UdpHostPorts      []string

	// Remaining command line arguments (the command to execute)
	Args []string
}

func ParseFlags(defaultConfigPath string) (*Flags, error) {
	flag.Usage = func() {
		envId, err := jailfs.CwdToEnvId()
		defaultEnvId := ""
		if err == nil {
			defaultEnvId = fmt.Sprintf(" (default: %s)", envId)
		}

		fmt.Fprintf(os.Stderr, `Drop limits programs abilities to read and write user's files
Usage: drop [options] [command...]
Options:
  -env, -e value
        Environment ID%s
  -config, -c value
        Path to TOML config file (default: %s)
  -root, -r
        Be root (uid 0) in the jail. Useful for running installation scripts that
        require to be run as root. This option doesn't grant any additional privileges to the jailed
        processes. For convenience, the home dir of a root user is not set to /root, but
        kept as the original home dir.
  -version
        Print program version

Environments management:
  -ls, -l
        List available Drop environments
  -rm
        Remove Drop environment

Mounts related options:
  -no-cwd, -nc
        Ignore cwd.mounts entries from config - do not make the current
        working directory available in the sandbox unless some other mount
        entry exposes the CWD.
  -mount, -m value
        Add a mount to the list of mounts from the TOML config file.
        The flag can be repeated.
        Format: source[:target][:rw]
        Examples: -m /mnt -m /tmp:/host-tmp -m ~/my-project::rw

Networking options:
  -net, -n value
        Network mode: off or isolated

  Port publishing from the sandbox:
    -tcp-publish, -t value
          Publish a TCP port from the sandbox.
    -udp-publish, -u value
          Publish a UDP port from the sandbox.
     Format: [hostIP/]hostPort[:sandboxPort]
     By default the published ports are bound only to localhost, to
     bind a port to all available IP addresses pass 0.0.0.0 as the
     hostIP.
     A value "auto" automatically publishes all ports bound in the
     sandbox on ALL available IP addresses (use "auto" only with
     firewall blocking external connection to the machine).

  Making host ports bound to localhost available in the sandbox:
    -tcp-host, -T value
          Make a TCP port from the host available in the sandbox.
    -udp-host, -U value
          Make a UDP port from the host available in the sandbox.
     Format: hostPort[:sandboxPort]
     A value "auto" makes all the localhost ports available in the
     sandbox.

  All port forwarding flags can be repeated.
  Ports configured via flags add to the ports configured via the
  config file.

  -help, -h
        Show help
`, defaultEnvId, defaultConfigPath)
	}
	var f Flags
	flag.StringVar(&f.EnvId, "env", "", "")
	flag.StringVar(&f.EnvId, "e", "", "")
	flag.StringVar(&f.ConfigPath, "config", "", "")
	flag.StringVar(&f.ConfigPath, "c", "", "")
	flag.BoolVar(&f.Version, "version", false, "")

	flag.BoolVar(&f.Ls, "ls", false, "")
	flag.BoolVar(&f.Ls, "l", false, "")
	flag.StringVar(&f.Rm, "rm", "", "")

	flag.BoolVar(&f.NoCwd, "no-cwd", false, "")
	flag.BoolVar(&f.NoCwd, "nc", false, "")
	flag.Var((*stringSlice)(&f.Mounts), "mount", "")
	flag.Var((*stringSlice)(&f.Mounts), "m", "")

	flag.BoolVar(&f.BeRoot, "root", false, "")
	flag.BoolVar(&f.BeRoot, "r", false, "")
	flag.StringVar(&f.NetworkMode, "net", "", "")
	flag.StringVar(&f.NetworkMode, "n", "", "")
	flag.Var((*stringSlice)(&f.TcpPublishedPorts), "tcp-publish", "")
	flag.Var((*stringSlice)(&f.TcpPublishedPorts), "t", "")
	flag.Var((*stringSlice)(&f.TcpHostPorts), "tcp-host", "")
	flag.Var((*stringSlice)(&f.TcpHostPorts), "T", "")
	flag.Var((*stringSlice)(&f.UdpPublishedPorts), "udp-publish", "")
	flag.Var((*stringSlice)(&f.UdpPublishedPorts), "u", "")
	flag.Var((*stringSlice)(&f.UdpHostPorts), "udp-host", "")
	flag.Var((*stringSlice)(&f.UdpHostPorts), "U", "")

	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("failed to parse command line: %v", err)
	}
	if f.ConfigPath == "" {
		f.ConfigPath = defaultConfigPath
	}
	if f.EnvId == "" {
		envId, err := jailfs.CwdToEnvId()
		if err != nil {
			return nil, err
		}
		f.EnvId = envId
	}

	if !jailfs.IsEnvIdValid(f.EnvId) {
		return nil, fmt.Errorf("invalid character in env ID")
	}

	f.Args = flag.Args()
	return &f, nil
}

// FlagsToConfig modifies cfg from a TOML file with values passed via
// command line flags. The function validates config after the
// modification.
func FlagsToConfig(cfg *config.Config, flags *Flags) error {
	for _, m := range flags.Mounts {
		mount, err := config.ParseMountCompact(m)
		if err != nil {
			return fmt.Errorf("command line -mount flag: %v", err)
		}
		cfg.Mounts = append(cfg.Mounts, *mount)
	}

	if flags.NetworkMode != "" {
		cfg.Net.Mode = flags.NetworkMode
	}
	if len(flags.TcpPublishedPorts) > 0 {
		p, err := parsePublishPortFlags(flags.TcpPublishedPorts)
		if err != nil {
			return fmt.Errorf("command line -tcp-publish flag: %v", err)
		}
		cfg.Net.TCPPublishedPorts = append(cfg.Net.TCPPublishedPorts, p...)
	}
	if len(flags.TcpHostPorts) > 0 {
		p, err := parseHostPortFlags(flags.TcpHostPorts)
		if err != nil {
			return fmt.Errorf("command line -tcp-host flag: %v", err)
		}
		cfg.Net.TCPHostPorts = append(cfg.Net.TCPHostPorts, p...)

	}
	if len(flags.UdpPublishedPorts) > 0 {
		p, err := parsePublishPortFlags(flags.UdpPublishedPorts)
		if err != nil {
			return fmt.Errorf("command line -udp-publish flag: %v", err)
		}
		cfg.Net.UDPPublishedPorts = append(cfg.Net.UDPPublishedPorts, p...)
	}
	if len(flags.UdpHostPorts) > 0 {
		p, err := parseHostPortFlags(flags.UdpHostPorts)
		if err != nil {
			return fmt.Errorf("command line -udp-host flag: %v", err)
		}
		cfg.Net.UDPHostPorts = append(cfg.Net.UDPHostPorts, p...)
	}
	if flags.NoCwd {
		cfg.Cwd.Mounts = nil
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
