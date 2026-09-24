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

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wrr/drop/internal/osutil"
)

const defaultConfigPerms = 0600

type DefaultMount struct {
	Entry   string
	Comment string
}

// WriteBase writes a default base config file to path.
func WriteBase(path string, homeDir string) error {
	// mounts contains files to expose from home dir, these
	// files are included in the generated default config only if they exist
	// in the user's home.
	mounts := []DefaultMount{
		{"~/.ackrc", ""},
		{"~/.emacs", ""},
		{"~/.profile", ""},
		{"~/.gitconfig", "Remove if you keep secrets in .gitconfig"},
		{"~/.nvm", ""},
		{"~/.screenrc", ""},
		{"~/.bashrc", "Ensure there are no secrets in your shell config files"},
		{"~/.bash_logout", ""},
		{"~/.bash_profile", ""},
		{"~/.zshenv", ""},
		{"~/.zlogin", ""},
		{"~/.zprofile", ""},
		{"~/.zlogout", ""},
		{"~/.zshrc", ""},
		{"~/.local/bin:~/.local-host/bin", "Rename .local/bin from host, so the sandbox has its own writable .local/bin"},
		{"~/.local/include:~/.local-host/include", ""},
		{"~/.local/lib:~/.local-host/lib", ""},
		{"~/go/bin", "Commands installed by go install"},
		// These need to be mounted in .local, not .local-host, because uv
		// and pipx put absolute path symlinks in .local/bin that point to
		// .local/share/xxx
		//
		// These are also read-only, but env vars below instruct uv and
		// pipx to install packages within the sandbox to
		// .local/share/(uv|pipx)-drop
		{"~/.local/share/uv", "Packages installed by uv"},
		{"~/.local/pipx", "Packages installed by pipx (old location)"},
		{"~/.local/share/pipx", "Packages installed by pipx"},

		{"~/.cargo/bin", "Commands installed by cargo install, and rustup"},
		{"~/.cargo/env", "Sourced by shell config files"},
		{"~/.cargo/env.fish", ""},
		{"~/.cargo/env.nu", ""},
		{"~/.cargo/config.toml", "Remove if you keep secrets in cargo config"},
		{"~/.rustup", "Rust toolchains"},
	}

	mounts = keepExistingEntries(mounts, homeDir)

	defaultConfig := fmt.Sprintf(`################################################################
# Drop sandbox base configuration file.
# Environment-specific config files by default extend this file.
################################################################

# Sandboxing runtime:
# "native" - run directly on the host kernel.
# "gvisor" - for added isolation run on the gVisor user-space kernel.
runtime = "native"

# Directories and files exposed to Drop.
#
# Entries can have a compact string syntax, like:
#
# "~/bin" - expose the ~/bin directory as read-only. Directories are
#           exposed with all content, including subdirectories.
# "~/bin:~/bin-host" - expose the ~/bin dir as read-only ~/bin-host.
# "~/plan::rw" - expose the ~/plan file as writable.
# "~/plan:~/plan-host:rw" - expose the ~/plan as writable ~/plan-host.
#
# Alternatively, a verbose dictionary syntax can be used; it allows
# handling paths with ':' characters. Equivalents of the examples
# above with the verbose syntax are:
#
# {source="~/bin"}
# {source="~/bin", target="~/bin-host"}
# {source="~/plan", rw=true}
# {source="~/plan", target="~/plan-host", rw=true}
#
# All paths must be normalized and either start with / or ~/.
#
# Be sure not to expose files with secrets or other sensitive
# data. Configs without sensitive data are safe to expose as read-only.
#
# Use files exposed as read-write carefully and sparingly - untrusted
# programs should not be able to write files that are executed outside
# of the sandbox. Shell config scripts are executed by the host, so
# exposing them as read-write would let sandboxed programs inject
# commands that run outside the sandbox. Read-only is safe.
# Similarly, entries from ~/.bash_history can be executed, so it is
# best not to expose history, but allow shells in Drop environments
# to create isolated history files, one per environment.
mounts = %s

# Paths to dirs or files to block access to.
#
# Host filesystem access restrictions still apply in Drop, so you
# don't need to block files your current user already can't access
# (for example /etc/shadow). Drop also mounts almost
# all dirs read-only, so you don't need to include files just to block
# writing to them.
blocked_paths = []

[environ]
# Environment variables to expose from the process starting Drop to
# the sandbox. You can use glob patterns to expose all variables with
# a common prefix/suffix.
#
# Do not expose variables containing secrets.
exposed_vars = [
  "XDG_DATA_HOME",
  "XDG_CONFIG_HOME",
  "XDG_STATE_HOME",
  "XDG_DATA_DIRS",
  "XDG_CONFIG_DIRS",
  "XDG_CACHE_HOME",
  "XDG_RUNTIME_DIR",
  "SHELL",
  "LC_*",
  "XTERM_SHELL",
  "EDITOR",
  "PWD",
  "LOGNAME",
  "HOME",
  "LANG",
  "LESSCLOSE",
  "LESSOPEN",
  "LS_COLORS",
  "XTERM_LOCALE",
  "TERM",
  "USER",
  "SHLVL",
  "PATH",
]

# New environment variables passed to the sandboxed process.
# Values can include existing vars as ${VAR_NAME}.
set_vars = [
  "debian_chroot=drop", # Add '(drop)' prefix to shell prompts on Debian-based systems
  "PATH=${PATH}:${HOME}/.local-host/bin", # Add .local/bin from host (mounted as .local-host/bin) to PATH.

  # Config vars for third-party package managers. These are used
  # together with mount rules above to achieve the following:
  # * Sandbox can execute host-installed packages.
  # * Sandbox can install and execute sandbox-only packages.
  # * Sandbox can't install or modify any host-visible packages.

  "GOBIN=${HOME}/.local/bin", # sanbox-only go commands
  "CARGO_INSTALL_ROOT=${HOME}/.local", # cargo commands
  # uv installed packages, Python versions and executables:
  "UV_TOOL_DIR=${HOME}/.local/share/uv-drop/tools",
  "UV_PYTHON_INSTALL_DIR=${HOME}/.local/share/uv-drop/python",
  "UV_TOOL_BIN_DIR=${HOME}/.local/bin",
  "PIPX_HOME=${HOME}/.local/share/pipx-drop", # pipx packages
]

[net]
# Network mode:
# "off"      - programs in the sandbox cannot access remote or local
#              network services. Ports opened by the programs are not
#              accessible from the host.
# "isolated" - programs in the sandbox can access remote services.
#              Port mapping settings below determine which services
#              running in the sandbox can be accessed from the host and
#              which services running on the host can be accessed from
#              the sandbox.
mode = "isolated"

# TCP ports published from the sandbox.
#
# Entries have the form: [host_ip/][HOST_PORT:]DROP_PORT
# If host_ip is not specified, it defaults to 127.0.0.1.
# If HOST_PORT is not specified, it defaults to DROP_PORT.
# An empty list means no ports are exposed.
# Example valid list items:
# "8080" - publish port 8080 from the sandbox as 127.0.0.1:8080 on the host
# "8080:8000" - publish port 8000 from the sandbox as 127.0.0.1:8080
#               on the host
# "0.0.0.0/8080:8000" - publish port 8000 from the sandbox as 8080 on
#                       the host, bind it to all the host's IP
#                       addresses. This makes the port externally
#                       accessible if the host has no firewall rules
#                       to block outside traffic to this port
# "127.0.0.1/auto" - all ports open in the sandbox are automatically
#                    published and bound to the host's localhost
#                    address. This is preferable to the plain "auto"
#                    option below when external exposure is not needed,
#                    but requires pasta version 2026_05_07.1afd4ed or
#                    newer.
# "auto" - all ports open in the sandbox are automatically published
#          and bound to ALL the host's IP addresses. This is
#          convenient, but must be used with care; make sure the host
#          has a firewall configured to filter outside traffic.
tcp_published_ports = []
# UDP ports published from the sandbox.
udp_published_ports = []

# Localhost TCP ports open on the host that the sandbox can access.
# Entries have the form
# HOST_PORT[:DROP_PORT]
# If DROP_PORT is not specified, it defaults to HOST_PORT
tcp_host_ports = []
# Localhost UDP ports open on the host that the sandbox can access.
udp_host_ports = []
`, mountEntriesToToml(mounts))

	if err := osutil.MkdirAll(filepath.Dir(path)); err != nil {
		return err
	}

	if err := os.WriteFile(path, []byte(defaultConfig), defaultConfigPerms); err != nil {
		return fmt.Errorf("write base config to %v: %v", path, err)
	}

	return nil
}

// WriteDefaultForEnv writes a default config file for a new drop
// environment to path.
func WriteDefaultForEnv(path string, mounts []DefaultMount, homeDir string) error {
	mounts = keepExistingEntries(mounts, homeDir)
	envConfig := fmt.Sprintf(`#######################################################
# Drop sandbox environment-specific configuration file.
#######################################################

# Use all the settings from the base.toml file. All the list settings
# set in this file are appended to the settings from the base.toml.
# The runtime and net.mode, if set in this file, override the
# settings from the base.toml.
extends = "./base.toml"

# Add any settings that apply to this environment only:

mounts = %s

blocked_paths = []

[environ]
exposed_vars = []
set_vars = []

[net]
# Uncomment to disable network access for this environment:
# mode = "off"

tcp_published_ports = []
udp_published_ports = []
tcp_host_ports = []
udp_host_ports = []
`, mountEntriesToToml(mounts))

	if err := osutil.MkdirAll(filepath.Dir(path)); err != nil {
		return err
	}

	if err := os.WriteFile(path, []byte(envConfig), defaultConfigPerms); err != nil {
		return fmt.Errorf("write environment config to %v: %v", path, err)
	}

	return nil
}

func keepExistingEntries(entries []DefaultMount, homeDir string) []DefaultMount {
	var existing []DefaultMount
	for _, e := range entries {
		src := strings.SplitN(e.Entry, ":", 2)[0]
		path := osutil.TildeToHomeDir(src, homeDir)
		if osutil.CanStat(path) {
			existing = append(existing, e)
		}
	}
	return existing
}

func mountEntriesToToml(entries []DefaultMount) string {
	if len(entries) == 0 {
		return "[]"
	}
	lines := []string{"["}
	for _, e := range entries {
		if e.Comment != "" {
			lines = append(lines, fmt.Sprintf("  %q, # %s", e.Entry, e.Comment))
		} else {
			lines = append(lines, fmt.Sprintf("  %q,", e.Entry))
		}
	}
	lines = append(lines, "]")
	return strings.Join(lines, "\n")
}
