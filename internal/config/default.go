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

// WriteDefault creates a default config in ~/.drop/config.
func WriteDefault(path string, homeDir string) error {
	// pathsRODefault contains files to expose from home dir, these
	// files are included in the generated default config only if they exist
	// in the user's home.
	pathsRODefault := []configFileEntry{
		{"~/.ackrc", ""},
		{"~/.emacs", ""},
		{"~/.profile", ""},
		{"~/.gitconfig", " # Remove if you keep secrets in .gitconfig"},
		{"~/.nvm", ""},
		{"~/.screenrc", ""},
		{"~/.bashrc", " # Ensure there are no secrets in your shell config files"},
		{"~/.bash_logout", ""},
		{"~/.bash_profile", ""},
		{"~/.zshenv", ""},
		{"~/.zlogin", ""},
		{"~/.zprofile", ""},
		{"~/.zlogout", ""},
		{"~/.zshrc", ""},
	}

	// blockedDefault contains paths to block, also included only if
	// they exist.
	blockedDefault := []configFileEntry{
		{"/mnt", ""},
		{"/media", ""},
		{"/snap", ""},
		{"/cdrom", ""},
	}

	pathsRODefault = keepExistingEntries(homeDir, pathsRODefault)
	blockedDefault = keepExistingEntries("/", blockedDefault)

	defaultConfig := fmt.Sprintf(`# Drop sandboxing configuration file

# Directories and files from the user home exposed to Drop in read-only mode.
#
# Be sure not to expose files with keys or other sensitive data to the
# sandbox. Configs without sensitive data are safe to expose and will ensure
# the Drop environment doesn't impede work.
paths_ro = %s

# Directories and files from the user home exposed to Drop in read-write mode.
#
# Be careful, it is best to keep this list empty. Untrusted programs
# in the sandbox should not be able to write any files that are executed
# outside of the sandbox, so for sure do not expose dirs like bin or
# shell config files in writable mode. Bash history should also not be
# exposed, because items from the history can be executed.
paths_rw = []

# Absolute paths to dirs or files to block access to.
#
# All host filesystem access restrictions still apply to Drop, so you
# don't need to block access to files that are already not accessible
# to your current user (for example /root). Drop also mounts almost
# the whole filesystem read-only, so you don't need to include files
# just to block writing to them.
blocked = %s

# Environment variables to be exposed from process starting Drop to
# the sandbox. You can use glob patterns to expose all variables with
# common prefix/suffix.
#
# Do not expose variables that contain sensitive secrets, but other
# than that, expose all variables needed to ensure convenient work in
# the Drop environment.
env_expose = [
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

[net]
# Network mode:
# "off" - programs in the sandbox cannot access remote and local
#         network services. Ports opened by the programs are not
#         accessible from the host.
# "isolated" - programs in the sandbox can access remote services.
#              Port mapping settings below determine which services
#              running in the sandbox can be accessed from the host and
#              which services running on the host can be accessed from
#              the sandbox.
# "unjailed" - Drop shares networking with the host, can access local
#              and remote services. This can be useful to run trusted
#              program using Drop filesystem organization, but without
#              any additional restrictions. It does not provide proper
#              sandboxing.
mode = "isolated"

# TCP ports exposed from the Drop sandbox to the host.
# Empty list means no ports are exposed.
# Example, valid list items:
# "auto" - all ports open in the sandbox can be accessed from the host
#          using the same port number.
# "8080" - expose port 8080 from the sandbox as port 8080 on the host
# "8080:8000" - expose port 8000 from the sandbox, map it to port 8080
#               on the host
# "127.0.0.1/8080:8000" - same as above, but only bind the host port
#                         to loopback interface
tcp_ports_to_host = ["auto"]

# TCP ports exposed from the host to the Drop sandbox.
tcp_ports_from_host = []

# UDP ports exposed from the Drop sandbox to the host.
udp_ports_to_host = []

# UDP ports exposed from the host to the Drop sandbox.
udp_ports_from_host = []
`, toTomlString(pathsRODefault), toTomlString(blockedDefault))

	if err := osutil.MkdirAll(filepath.Dir(path)); err != nil {
		return err
	}

	if err := os.WriteFile(path, []byte(defaultConfig), 0644); err != nil {
		return fmt.Errorf("failed to write default config: %v", err)
	}

	return nil
}

func keepExistingEntries(pathPrefix string, entries []configFileEntry) []configFileEntry {
	var existing []configFileEntry
	for _, entry := range entries {
		fullPath := filepath.Join(pathPrefix, entry.path)
		if osutil.Exists(fullPath) {
			existing = append(existing, entry)
		}
	}
	return existing
}

func toTomlString(entries []configFileEntry) string {
	if len(entries) == 0 {
		return "[]"
	}
	lines := []string{"["}
	for _, entry := range entries {
		lines = append(lines, fmt.Sprintf("  \"%s\",%s", entry.path, entry.comment))
	}
	lines = append(lines, "]")

	return strings.Join(lines, "\n")
}
