package config

import (
	"strings"
	"testing"
)

// expectStringSlicesEqual reports error if thw string slices differ
func expectStringSlicesEqual(t *testing.T, fieldName string, actual, expected []string) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Errorf("expected %s length %d, got %d", fieldName, len(expected), len(actual))
		return
	}
	for i, expectedItem := range expected {
		if actual[i] != expectedItem {
			t.Errorf("expected %s[%d] %s, got %s", fieldName, i, expectedItem, actual[i])
		}
	}
}

// expectMountSlicesEqual reports error if two Mount slices differ
func expectMountSlicesEqual(t *testing.T, fieldName string, actual, expected []Mount) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Errorf("expected %s length %d, got %d", fieldName, len(expected), len(actual))
		return
	}
	for i, expectedItem := range expected {
		if actual[i] != expectedItem {
			t.Errorf("expected %s[%d] = %+v, got %+v", fieldName, i, expectedItem, actual[i])
		}
	}
}

func TestValidatePortForward(t *testing.T) {
	tests := []struct {
		name      string
		portSpecs []string
		error     string
	}{
		{
			name:      "single port",
			portSpecs: []string{"8080"},
			error:     "",
		},
		{
			name:      "host:guest port mapping",
			portSpecs: []string{"8080:80"},
			error:     "",
		},
		{
			name:      "IP/host:guest port mapping",
			portSpecs: []string{"127.0.0.1/8080:80"},
			error:     "",
		},
		{
			name:      "Multiple valid rules",
			portSpecs: []string{"8080", "8080:80", "127.0.0.1/8080:80", "1200"},
			error:     "",
		},
		{
			name:      "invalid port number - non-numeric",
			portSpecs: []string{"8080:80", "abc"},
			error:     "invalid port number 'abc'",
		},
		{
			name:      "invalid port number - out of range low",
			portSpecs: []string{"0"},
			error:     "port number out of range: 0",
		},
		{
			name:      "invalid port number - out of range high",
			portSpecs: []string{"65536"},
			error:     "port number out of range: 65536",
		},
		{
			name:      "invalid guest port - non-numeric",
			portSpecs: []string{"8080:abc"},
			error:     "invalid port number 'abc'",
		},
		{
			name:      "invalid IP address",
			portSpecs: []string{"invalid.ip/8080:80"},
			error:     "invalid port forwarding IP address: invalid.ip",
		},
		{
			name:      "Multiple IP addresses",
			portSpecs: []string{"127.0.0.1/8080:127.0.0.1/8080"},
			error:     "invalid port forwarding format",
		},
		{
			name:      "too many parts",
			portSpecs: []string{"127.0.0.1/8080:80:443"},
			error:     "invalid port forwarding format",
		},
		{
			name:      "empty string",
			portSpecs: []string{""},
			error:     "invalid port number",
		},
		{
			name:      "minimum valid port",
			portSpecs: []string{"1"},
			error:     "",
		},
		{
			name:      "maximum valid port",
			portSpecs: []string{"65535"},
			error:     "",
		},
		{
			name:      "IP with single port",
			portSpecs: []string{"192.168.1.1/8000"},
			error:     "",
		},
		{
			name:      "Three ports",
			portSpecs: []string{"124:200:8000"},
			error:     "invalid port forwarding format",
		},
		{
			name:      "auto with other ports",
			portSpecs: []string{"auto", "8080"},
			error:     "\"auto\" must be the only port forwarding rule",
		},
		{
			name:      "none with other ports",
			portSpecs: []string{"none", "8080:80"},
			error:     "\"none\" must be the only port forwarding rule",
		},
		{
			name:      "auto alone",
			portSpecs: []string{"auto"},
			error:     "",
		},
		{
			name:      "none alone",
			portSpecs: []string{"none"},
			error:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePortForward(tt.portSpecs)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error '%s', got nil", tt.error)
					return
				}
				if !strings.Contains(err.Error(), tt.error) {
					t.Errorf("expected error containing '%s', got '%s'", tt.error, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestParseNetConfig(t *testing.T) {
	tests := []struct {
		name     string
		tomlStr  string
		expected Config
		error    string
	}{
		{
			name: "valid net config with all fields",
			tomlStr: `
[net]
mode = "isolated"
tcp_ports_to_host = ["auto"]
tcp_ports_from_host = ["8080", "3000:3001"]
udp_ports_to_host = ["5000"]
udp_ports_from_host = ["192.168.1.1/12000:1700", "9000"]
`,
			expected: Config{
				Net: Net{
					Mode:             "isolated",
					TCPPortsToHost:   []string{"auto"},
					TCPPortsFromHost: []string{"8080", "3000:3001"},
					UDPPortsToHost:   []string{"5000"},
					UDPPortsFromHost: []string{"192.168.1.1/12000:1700", "9000"},
				},
			},
			error: "",
		},
		{
			name: "empty net config",
			tomlStr: `
[net]
mode = "off"
tcp_ports_to_host = []
tcp_ports_from_host = []
udp_ports_to_host = []
udp_ports_from_host = []
`,
			expected: Config{
				Net: Net{
					Mode:             "off",
					TCPPortsToHost:   []string{},
					TCPPortsFromHost: []string{},
					UDPPortsToHost:   []string{},
					UDPPortsFromHost: []string{},
				},
			},
			error: "",
		},
		{
			name:    "no net section",
			tomlStr: ``,
			expected: Config{
				Net: Net{
					Mode:             "isolated", // default
					TCPPortsToHost:   nil,
					TCPPortsFromHost: nil,
					UDPPortsToHost:   nil,
					UDPPortsFromHost: nil,
				},
			},
			error: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Parse(tt.tomlStr)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error '%s', got nil", tt.error)
					return
				}
				if !strings.Contains(err.Error(), tt.error) {
					t.Errorf("expected error containing '%s', got '%s'", tt.error, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result == nil {
				t.Errorf("expected result but got nil")
				return
			}
			got := result.Net
			expected := tt.expected.Net
			if got.Mode != expected.Mode {
				t.Errorf("expected Mode '%s', got '%s'", expected.Mode, got.Mode)
			}
			expectStringSlicesEqual(t, "TCPPortsToHost", got.TCPPortsToHost, expected.TCPPortsToHost)
			expectStringSlicesEqual(t, "TCPPortsFromHost", got.TCPPortsFromHost, expected.TCPPortsFromHost)
			expectStringSlicesEqual(t, "UDPPortsToHost", got.UDPPortsToHost, expected.UDPPortsToHost)
			expectStringSlicesEqual(t, "UDPPortsFromHost", got.UDPPortsFromHost, expected.UDPPortsFromHost)
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		tomlStr  string
		expected Config
		error    string
	}{
		{
			name: "complete valid config",
			tomlStr: `
mounts = [
  "/home/user/docs",
  "/tmp:~/tmp:rw,overlay",
  "/home/user/work::rw",
  {source = "/media", target = "~/media", rw = true}
]
blocked = ["/mnt", "/root"]
env_expose = ["HOME", "PATH", "LC_*"]

[net]
mode = "isolated"
tcp_ports_to_host = ["8080", "3000:3001"]
tcp_ports_from_host = ["auto"]
udp_ports_to_host = ["5000"]
udp_ports_from_host = ["192.168.1.1/12000:1700", "9000"]
`,
			expected: Config{
				Mounts: []Mount{
					{Source: "/home/user/docs", Target: "/home/user/docs"},
					{Source: "/tmp", Target: "~/tmp", RW: true, Overlay: true},
					{Source: "/home/user/work", Target: "/home/user/work", RW: true},
					{Source: "/media", Target: "~/media", RW: true},
				},
				Blocked:   []string{"/mnt", "/root"},
				EnvExpose: []string{"HOME", "PATH", "LC_*"},
				Net: Net{
					Mode:             "isolated",
					TCPPortsToHost:   []string{"8080", "3000:3001"},
					TCPPortsFromHost: []string{"auto"},
					UDPPortsToHost:   []string{"5000"},
					UDPPortsFromHost: []string{"192.168.1.1/12000:1700", "9000"},
				},
			},
			error: "",
		},
		{
			name:    "empty config",
			tomlStr: ``,
			expected: Config{
				Mounts:    nil,
				Blocked:   nil,
				EnvExpose: nil,
				Net: Net{
					Mode:             "isolated", // default
					TCPPortsToHost:   nil,
					TCPPortsFromHost: nil,
					UDPPortsToHost:   nil,
					UDPPortsFromHost: nil,
				},
			},
			error: "",
		},
		{
			name: "invalid env_expose pattern",
			tomlStr: `
env_expose = ["HOME", "INVALID["]
`,
			expected: Config{},
			error:    "invalid env_expose pattern 'INVALID['",
		},
		{
			name: "invalid TOML syntax",
			tomlStr: `
home_visible = [invalid syntax
`,
			expected: Config{},
			error:    "failed to parse config",
		},
		{
			name: "invalid tcp_ports_to_host",
			tomlStr: `
[net]
tcp_ports_to_host = ["8080", "invalid_port"]
`,
			expected: Config{},
			error:    "invalid tcp_ports_to_host",
		},
		{
			name: "invalid tcp_ports_from_host",
			tomlStr: `
[net]
tcp_ports_from_host = ["8080", "auto"]
`,
			expected: Config{},
			error:    "invalid tcp_ports_from_host: \"auto\" must be the only port forwarding rule",
		},
		{
			name: "invalid udp_ports_to_host",
			tomlStr: `
[net]
udp_ports_to_host = ["0"]
`,
			expected: Config{},
			error:    "invalid udp_ports_to_host: port number out of range: 0",
		},
		{
			name: "invalid udp_ports_from_host",
			tomlStr: `
[net]
udp_ports_from_host = ["invalid.ip/8080:80"]
`,
			expected: Config{},
			error:    "invalid udp_ports_from_host: invalid port forwarding IP address: invalid.ip",
		},
		{
			name: "invalid net mode",
			tomlStr: `
[net]
mode = "foo"
`,
			expected: Config{},
			error:    "invalid network mode 'foo': must be 'off', 'isolated', or 'unjailed'",
		},
		{
			name: "invalid mounts",
			tomlStr: `
mounts = ["/home/../invalid"]
`,
			expected: Config{},
			error:    "invalid mounts '/home/../invalid': path is not normalized",
		},
		{
			name: "invalid mounts, relative path",
			tomlStr: `
mounts = ["relative/path"]
`,
			expected: Config{},
			error:    "invalid mounts 'relative/path': path must start with / or ~/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Parse(tt.tomlStr)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error '%s', got nil", tt.error)
					return
				}
				if !strings.Contains(err.Error(), tt.error) {
					t.Errorf("expected error containing '%s', got '%s'", tt.error, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result == nil {
				t.Errorf("expected result but got nil")
				return
			}

			expectMountSlicesEqual(t, "Mounts", result.Mounts, tt.expected.Mounts)
			expectStringSlicesEqual(t, "Blocked", result.Blocked, tt.expected.Blocked)
			expectStringSlicesEqual(t, "EnvExpose", result.EnvExpose, tt.expected.EnvExpose)

			if result.Net.Mode != tt.expected.Net.Mode {
				t.Errorf("expected Net.Mode '%s', got '%s'", tt.expected.Net.Mode, result.Net.Mode)
			}
			expectStringSlicesEqual(t, "Net.TCPPortsToHost", result.Net.TCPPortsToHost, tt.expected.Net.TCPPortsToHost)
			expectStringSlicesEqual(t, "Net.TCPPortsFromHost", result.Net.TCPPortsFromHost, tt.expected.Net.TCPPortsFromHost)
			expectStringSlicesEqual(t, "Net.UDPPortsToHost", result.Net.UDPPortsToHost, tt.expected.Net.UDPPortsToHost)
			expectStringSlicesEqual(t, "Net.UDPPortsFromHost", result.Net.UDPPortsFromHost, tt.expected.Net.UDPPortsFromHost)
		})
	}
}

func TestValidateNetworkMode(t *testing.T) {
	validModes := []string{"off", "isolated", "unjailed"}
	for _, mode := range validModes {
		if err := ValidateNetworkMode(mode); err != nil {
			t.Errorf("expected no error for mode '%s', got: %v", mode, err)
		}
	}

	invalidModes := []string{"invalid", "", "OFF"}
	for _, mode := range invalidModes {
		err := ValidateNetworkMode(mode)
		if err == nil {
			t.Errorf("expected error for mode '%s'", mode)
		} else if !strings.Contains(err.Error(), "invalid network mode") {
			t.Errorf("unexpected error for mode '%s': %s", mode, err.Error())
		}
	}
}

func TestParseMount(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected Mount
		error    string
	}{
		{
			name:  "source only",
			input: "~/foo",
			expected: Mount{
				Source:  "~/foo",
				Target:  "~/foo",
				RW:      false,
				Overlay: false,
			},
		},
		{
			name:  "source and target",
			input: "~/foo:~/bar",
			expected: Mount{
				Source:  "~/foo",
				Target:  "~/bar",
				RW:      false,
				Overlay: false,
			},
		},
		{
			name:  "source with empty target",
			input: "~/foo:",
			expected: Mount{
				Source:  "~/foo",
				Target:  "~/foo",
				RW:      false,
				Overlay: false,
			},
		},
		{
			name:  "source, target, and rw option",
			input: "~/foo:~/bar:rw",
			expected: Mount{
				Source:  "~/foo",
				Target:  "~/bar",
				RW:      true,
				Overlay: false,
			},
		},
		{
			name:  "source, target, and overlay option",
			input: "~/foo:~/bar:overlay",
			expected: Mount{
				Source:  "~/foo",
				Target:  "~/bar",
				RW:      false,
				Overlay: true,
			},
		},
		{
			name:  "source, target, and multiple options",
			input: "~/foo:~/bar:rw,overlay",
			expected: Mount{
				Source:  "~/foo",
				Target:  "~/bar",
				RW:      true,
				Overlay: true,
			},
		},
		{
			name:  "source with empty target and options",
			input: "~/foo::rw",
			expected: Mount{
				Source:  "~/foo",
				Target:  "~/foo",
				RW:      true,
				Overlay: false,
			},
		},
		{
			name:  "too many parts",
			input: "~/foo:~/bar:rw:extra",
			error: "mount config has too many parts separated by ':', should have at most 3: ~/foo:~/bar:rw:extra",
		},
		{
			name:  "invalid option",
			input: "~/foo:~/bar:ro",
			error: "not recognized mount option ro in ~/foo:~/bar:ro. Supported options are",
		},
		{
			name:  "one valid and one invalid option",
			input: "~/foo:~/bar:rw,bind",
			error: "not recognized mount option bind in ~/foo:~/bar:rw,bind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseMount(tt.input)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.error)
					return
				}
				if !strings.Contains(err.Error(), tt.error) {
					t.Errorf("expected error containing '%s', got '%s'", tt.error, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result == nil {
				t.Errorf("expected result but got nil")
				return
			}

			if *result != tt.expected {
				t.Errorf("expected %+v, got %+v", tt.expected, *result)
			}
		})
	}
}

func TestMountUnmarshalingAndValidation(t *testing.T) {
	tests := []struct {
		name     string
		tomlStr  string
		expected []Mount
		error    string
	}{
		{
			name: "string mounts",
			tomlStr: `
mounts = ["~/.bashrc", "/etc/hosts:~/hosts:rw"]
`,
			expected: []Mount{
				{Source: "~/.bashrc", Target: "~/.bashrc"},
				{Source: "/etc/hosts", Target: "~/hosts", RW: true},
			},
			error: "",
		},
		{
			name: "object with source only",
			tomlStr: `
mounts = [{source = "~/.bashrc"}, {source = "/etc/hosts", overlay=true}]
`,
			expected: []Mount{
				{Source: "~/.bashrc", Target: "~/.bashrc"},
				{Source: "/etc/hosts", Target: "/etc/hosts", Overlay: true},
			},
			error: "",
		},
		{
			name: "object with source and target",
			tomlStr: `
mounts = [{source = "~/.gitconfig", target = "~/.gitconfig-host"}, {source = "/boot", target = "/mnt/boot"}]
`,
			expected: []Mount{
				{Source: "~/.gitconfig", Target: "~/.gitconfig-host"},
				{Source: "/boot", Target: "/mnt/boot"},
			},
			error: "",
		},
		{
			name: "mixed string and objects",
			tomlStr: `
mounts = ["~/.bashrc", {source = "~/.gitconfig", target = "~/.gitconfig-host"}, {source = "/etc/hosts"}]
`,
			expected: []Mount{
				{Source: "~/.bashrc", Target: "~/.bashrc"},
				{Source: "~/.gitconfig", Target: "~/.gitconfig-host"},
				{Source: "/etc/hosts", Target: "/etc/hosts"},
			},
			error: "",
		},
		{
			name: "object with rw and overlay options",
			tomlStr: `
mounts = [{source = "~/foo", rw = true, overlay = true}]
`,
			expected: []Mount{
				{Source: "~/foo", Target: "~/foo", RW: true, Overlay: true},
			},
			error: "",
		},
		{
			name: "object with false rw and overlay",
			tomlStr: `
mounts = [{source = "~/foo", rw = false, overlay = false}]
`,
			expected: []Mount{
				{Source: "~/foo", Target: "~/foo", RW: false, Overlay: false},
			},
			error: "",
		},
		{
			name: "object without source field",
			tomlStr: `
mounts = [{target = "~/foo"}]
`,
			expected: []Mount{},
			error:    "mount config must have 'source' field",
		},
		{
			name: "object with non-string source",
			tomlStr: `
mounts = [{source = 123}]
`,
			expected: []Mount{},
			error:    "mount config 'source' must be a string",
		},
		{
			name: "object with non-string target",
			tomlStr: `
mounts = [{source = "~/.bashrc", target = 456}]
`,
			expected: []Mount{},
			error:    "mount config 'target' must be a string",
		},
		{
			name: "object with non-boolean rw",
			tomlStr: `
mounts = [{source = "~/.bashrc", rw = "true"}]
`,
			expected: []Mount{},
			error:    "mount config 'rw' must be a boolean",
		},
		{
			name: "object with non-boolean overlay",
			tomlStr: `
mounts = [{source = "~/.bashrc", overlay = 1}]
`,
			expected: []Mount{},
			error:    "mount config 'overlay' must be a boolean",
		},
		{
			name: "neither string nor object",
			tomlStr: `
mounts = [64]
`,
			expected: []Mount{},
			error:    "mount entry should be a string or an object, got int64",
		},
		// MountPath validation:
		{
			name: "Invalid path",
			tomlStr: `
mounts = ["~/.bashrc", "/etc/../hosts"]
`,
			expected: []Mount{},
			error:    "invalid mounts '/etc/../hosts': path is not normalized",
		},
		{
			name: "Invalid path, not normalized",
			tomlStr: `
mounts = ["~/.bashrc", "/etc/../hosts"]
`,
			expected: []Mount{},
			error:    "invalid mounts '/etc/../hosts': path is not normalized",
		},
		{
			name: "Invalid path, not absolute",
			tomlStr: `
mounts = ["~/.bashrc", {source = "/usr/bin", target = "bin"}]
`,
			expected: []Mount{},
			error:    "invalid mounts 'bin': path must start with / or ~/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Parse(tt.tomlStr)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.error)
					return
				}
				if !strings.Contains(err.Error(), tt.error) {
					t.Errorf("expected error containing '%s', got '%s'", tt.error, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result == nil {
				t.Errorf("expected result but got nil")
				return
			}

			expectMountSlicesEqual(t, "Mounts", result.Mounts, tt.expected)
		})
	}
}

func TestValidatePaths(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		error string
	}{
		{
			name:  "valid mixed paths",
			paths: []string{"/home/user/docs", "~/.gitconfig", "/tmp"},
			error: "",
		},
		{
			name:  "empty paths list",
			paths: []string{},
			error: "",
		},
		{
			name:  "invalid path in list",
			paths: []string{"/valid/path", "foo", "~/valid"},
			error: "invalid test_prop 'foo': path must start with / or ~/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePaths("test_prop", tt.paths)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error '%s', got nil", tt.error)
					return
				}
				if !strings.Contains(err.Error(), tt.error) {
					t.Errorf("expected error '%s', got '%s'", tt.error, err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidatePathEntry(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		error string
	}{
		{
			name:  "absolute path",
			path:  "/usr/local",
			error: "",
		},
		{
			name:  "absolute path with trailing /",
			path:  "/usr/local/",
			error: "",
		},
		{
			name:  "home path",
			path:  "~/tmp/docs",
			error: "",
		},
		{
			name:  "home path with trailing /",
			path:  "~/tmp/docs/",
			error: "",
		},
		{
			name:  "home dot file",
			path:  "~/.bashrc",
			error: "",
		},
		{
			name:  "empty path",
			path:  "",
			error: "path must start with / or ~/",
		},
		{
			name:  "relative path without ~",
			path:  "docs/file.txt",
			error: "path must start with / or ~/",
		},
		{
			name:  "path with ..",
			path:  "/home/../etc/passwd",
			error: "path is not normalized",
		},
		{
			name:  "home path with ..",
			path:  "~/../secrets",
			error: "path is not normalized",
		},
		{
			name:  "path with /./",
			path:  "/home/./user",
			error: "path is not normalized",
		},
		{
			name:  "path ending with /.",
			path:  "/home/user/.",
			error: "path is not normalized",
		},
		{
			name:  "path with double slashes",
			path:  "/home//user",
			error: "path is not normalized",
		},
		{
			name:  "invalid ~",
			path:  "~user",
			error: "path must start with / or ~/",
		},
		{
			name:  "tilde alone",
			path:  "~",
			error: "path must start with / or ~/",
		},
		{
			name:  "root directory alone",
			path:  "/",
			error: "cannot expose the whole root directory",
		},
		{
			name:  "home directory alone",
			path:  "~/",
			error: "cannot expose the whole home directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePathEntry(tt.path)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error '%s', got nil", tt.error)
					return
				}
				if !strings.Contains(err.Error(), tt.error) {
					t.Errorf("expected error '%s', got '%s'", tt.error, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEnvExpose(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		error    string
	}{
		{
			name:     "valid patterns",
			patterns: []string{"HOME", "LC_*", "VAR_?", "PATH"},
			error:    "",
		},
		{
			name:     "empty patterns list",
			patterns: []string{},
			error:    "",
		},
		{
			name:     "nil patterns list",
			patterns: nil,
			error:    "",
		},
		{
			name:     "invalid pattern - unclosed bracket",
			patterns: []string{"HOME", "LC_["},
			error:    "invalid env_expose pattern 'LC_['",
		},
		{
			name:     "invalid pattern - unclosed bracket at end",
			patterns: []string{"HOME", "PATH["},
			error:    "invalid env_expose pattern 'PATH['",
		},
		{
			name:     "multiple invalid patterns",
			patterns: []string{"HOME", "LC_[", "VALID_*", "BAD["},
			error:    "invalid env_expose pattern 'LC_['",
		},
		{
			name:     "valid character class pattern",
			patterns: []string{"VAR_[0-9]", "LC_[A-Z]*", "HOME"},
			error:    "",
		},
		{
			name:     "valid complex glob patterns",
			patterns: []string{"*_VAR", "PREFIX_*_SUFFIX", "VAR??", "LC_*"},
			error:    "",
		},
		{
			name:     "single invalid pattern",
			patterns: []string{"INVALID["},
			error:    "invalid env_expose pattern 'INVALID['",
		},
		{
			name:     "valid edge case patterns",
			patterns: []string{"*", "?", "[a-z]", "[0-9]*", "VAR_[!0-9]"},
			error:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvExpose(tt.patterns)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error '%s', got nil", tt.error)
					return
				}
				if !strings.Contains(err.Error(), tt.error) {
					t.Errorf("expected error '%s', got '%s'", tt.error, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
