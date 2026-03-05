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

type configFileEntry struct {
	path    string
	comment string
}

// WriteDefault writes a default config file to path.
func WriteDefault(path string, homeDir string) error {
	// pathsRODefault contains files to expose from home dir, these
	// files are included in the generated default config only if they exist
	// in the user's home.
	pathsRODefault := []configFileEntry{
		{"~/.ackrc", ""},
		{"~/.emacs", ""},
		{"~/.profile", ""},
		{"~/.gitconfig", "Remove if you keep secrets in .gitconfig"},
		{"~/go", ""},
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
	}

	pathsRODefault = keepExistingEntries(pathsRODefault, homeDir)

	defaultConfig := fmt.Sprintf(`# Drop sandbox configuration file

# Directories and files exposed to Drop.
#
# Entries can have a compact string syntax, like:
#
# "~/bin" - expose ~/bin directory as read-only. Directories are
#           exposed with all content, including sub-directories.
# "~/bin:~/host-bin" - expose ~/bin directory as read-only ~/host-bin.
# "~/plan::rw" - expose ~/plan file as writable.
# "~/plan:~/host-plan:rw" - expose ~/plan file as writable ~/host-plan.
#
# Alternatively, a verbose dictionary syntax can be used; it allows to
# handle paths with ':' characters. Equivalents of the examples above
# with the verbose syntax are:
#
# {source="~/bin"}
# {source="~/bin", target="~/host-bin"}
# {source="~/plan", rw=true}
# {source="~/plan", target="~/host-plan", rw=true}
#
# All paths must be normalized and either start with / or ~/.
#
# Be sure not to expose files with secrets or other sensitive
# data. Configs without sensitive data are safe to expose as read-only
# and make Drop more convienient to use.
#
# Use files exposed as read-write carefully and sparingly - untrusted
# programs should not be able to write files that are executed outside
# of the sandbox. Shell config scripts are executed, so it is safe to
# expose them as read-only, but not as read-write.  Similarly, entries
# from ~/.bash_history can be executed, so it is best not to expose
# history, but allow shells in Drop environments to create isolated
# history files, one per each environment.
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
# common prefix/suffix.
#
# Do not expose variables containing secrets. Expose all
# other variables needed for convenient work in Drop.
exposed_vars = [
  "XDG_DATA_HOME",
  "XDG_CONFIG_HOME",
  "XDG_STATE_HOME",
  "XDG_DATA_DIRS",
  "XDG_CONFIG_DIRS",
  "XDG_CACHE_HOME",
  "XDG_RUNTIME_DIRS",
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
  "SHLLVL",
  "PATH",
]

set_vars = [
  "debian_chroot=drop", # Add '(drop)' prefix to shell prompts on Debian-based systems
]

[net]
# Network mode:
# "off"      - programs in the sandbox cannot access remote and local
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
# If HOST_PORT is not specified it equals to DROP_PORT.
# Empty list means no ports are exposed.
# Example valid list items:
# "8080" - publish port 8080 from the sandbox as 127.0.0.1:8080 on the host
# "8080:8000" - publish port 8000 from the sandbox as 127.0.0.1:8080
#               on the host
# "0.0.0.0/8080:8000" - publish port 8000 from the sandbox as 8080 on
#                       the host, bind it to all the host's IP
#                       addresses. This makes the port externally
#                       accessible if the host has no firewall rules
#                       to block outside traffic to this port
# "auto" - all ports open in the sandbox are automatically published
#          and bound to ALL the host's IP addresses. This is
#          convienient, but must be used with care, make sure the host
#          has firewall configured to filter outside traffic
tcp_published_ports = []
# UDP ports published exposed from the sandbox.
udp_published_ports = []

# Localhost TCP ports open on the host that the sandbox can access.
# Entries have the form
# HOST_PORT[:DROP_PORT]
# If DROP_PORT is not specified, it equals to HOST_PORT
tcp_host_ports = []
# Localhost UDP ports open on the host that the sandbox can access.
udp_host_ports = []
`, sliceToToml(pathsRODefault, formatConfigFile))

	if err := osutil.MkdirAll(filepath.Dir(path)); err != nil {
		return err
	}

	if err := os.WriteFile(path, []byte(defaultConfig), 0644); err != nil {
		return fmt.Errorf("failed to write default config: %v", err)
	}

	return nil
}

// WriteDefaultForEnv writes a default config file for a new drop
// environment to path.
func WriteDefaultForEnv(path string, mounts []string) error {
	envConfig := fmt.Sprintf(`# Drop environment configuration file
extends = "./base.toml"

mounts = %s

blocked_paths = []

[environ]
exposed_vars = []

set_vars = []

[net]
# mode = "off"

tcp_published_ports = []
udp_published_ports = []
tcp_host_ports = []
udp_host_ports = []
`, sliceToToml(mounts, formatString))

	if err := osutil.MkdirAll(filepath.Dir(path)); err != nil {
		return err
	}

	if err := os.WriteFile(path, []byte(envConfig), 0644); err != nil {
		return fmt.Errorf("failed to write environment config: %v", err)
	}

	return nil
}

func keepExistingEntries(entries []configFileEntry, homeDir string) []configFileEntry {
	var existing []configFileEntry
	for _, entry := range entries {
		path := osutil.TildeToHomeDir(entry.path, homeDir)
		if osutil.Exists(path) {
			existing = append(existing, entry)
		}
	}
	return existing
}

func sliceToToml[T any](entries []T, format func(T) string) string {
	if len(entries) == 0 {
		return "[]"
	}
	lines := []string{"["}
	for _, entry := range entries {
		lines = append(lines, fmt.Sprintf("  %s", format(entry)))
	}
	lines = append(lines, "]")
	return strings.Join(lines, "\n")
}

func formatString(s string) string {
	return fmt.Sprintf("%q,", s)
}

func formatConfigFile(e configFileEntry) string {
	if e.comment != "" {
		return fmt.Sprintf("%q, # %s", e.path, e.comment)
	}
	return formatString(e.path)
}
