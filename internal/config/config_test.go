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

package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wrr/drop/internal/osutil"
)

func expectSlicesEqual[T comparable](t *testing.T, fieldName string, actual, expected []T) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Errorf("expected %s length %d, got %d", fieldName, len(expected), len(actual))
		return
	}
	for i, expectedItem := range expected {
		if actual[i] != expectedItem {
			t.Errorf("expected %s[%d] %v, got %v", fieldName, i, expectedItem, actual[i])
		}
	}
}

func expectConfigsEqual(t *testing.T, actual *Config, expected *Config) {
	t.Helper()
	if actual.Extends != expected.Extends {
		t.Errorf("expected Extends '%s', got '%s'", expected.Extends, actual.Extends)
	}

	expectSlicesEqual(t, "Mounts", actual.Mounts, expected.Mounts)
	expectSlicesEqual(t, "BlockedPaths", actual.BlockedPaths, expected.BlockedPaths)
	expectSlicesEqual(t, "Environ.ExposedVars", actual.Environ.ExposedVars, expected.Environ.ExposedVars)
	expectSlicesEqual(t, "Environ.SetVars", actual.Environ.SetVars, expected.Environ.SetVars)
	if actual.Net.Mode != expected.Net.Mode {
		t.Errorf("expected Net.Mode '%s', got '%s'", expected.Net.Mode, actual.Net.Mode)
	}
	expectSlicesEqual(t, "Net.TCPPublishedPorts", actual.Net.TCPPublishedPorts, expected.Net.TCPPublishedPorts)
	expectSlicesEqual(t, "Net.TCPHostPorts", actual.Net.TCPHostPorts, expected.Net.TCPHostPorts)
	expectSlicesEqual(t, "Net.UDPPublishedPorts", actual.Net.UDPPublishedPorts, expected.Net.UDPPublishedPorts)
	expectSlicesEqual(t, "Net.UDPHostPorts", actual.Net.UDPHostPorts, expected.Net.UDPHostPorts)
}

func checkError(expected string, got error) error {
	if expected == "" {
		if got != nil {
			return fmt.Errorf("unexpected error: %v", got)
		}
		return nil
	}
	if got == nil {
		return fmt.Errorf("expected error %q, got nil", expected)
	}
	if !strings.Contains(got.Error(), expected) {
		return fmt.Errorf("expected error %q, got %q", expected, got.Error())
	}
	return nil
}

func parse(configStr string) (*Config, error) {
	r := &reader{
		files:    make(map[string]bool),
		readFile: nil,
	}
	return r.parse(configStr, "")
}

func TestParsePort(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		error    string
	}{
		{
			name:     "valid port - 1",
			input:    "1",
			expected: 1,
			error:    "",
		},
		{
			name:     "valid port - 65535",
			input:    "65535",
			expected: 65535,
			error:    "",
		},
		{
			name:     "valid port - 4000",
			input:    "4000",
			expected: 4000,
			error:    "",
		},
		{
			name:     "invalid port number - non-numeric",
			input:    "abc",
			expected: -1,
			error:    "invalid port number 'abc'",
		},
		{
			name:     "invalid port number - out of range low",
			input:    "0",
			expected: -1,
			error:    "port number out of range: 0",
		},
		{
			name:     "invalid port number - out of range high",
			input:    "65536",
			expected: -1,
			error:    "port number out of range: 65536",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parsePort(tt.input)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestParsePublishedPort(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected PublishedPort
		error    string
	}{
		{
			name:  "single port",
			input: "8080",
			expected: PublishedPort{
				HostIP:    "127.0.0.1",
				HostPort:  8080,
				GuestPort: 8080,
			},
			error: "",
		},
		{
			name:  "host:guest port mapping",
			input: "8080:80",
			expected: PublishedPort{
				HostIP:    "127.0.0.1",
				HostPort:  8080,
				GuestPort: 80,
			},
			error: "",
		},
		{
			name:  "IP/host:guest port mapping",
			input: "192.168.1.12/8080:80",
			expected: PublishedPort{
				HostIP:    "192.168.1.12",
				HostPort:  8080,
				GuestPort: 80,
			},
			error: "",
		},
		{
			name:     "invalid port number - non-numeric",
			input:    "abc",
			expected: PublishedPort{},
			error:    "invalid port number 'abc'",
		},
		{
			name:     "invalid port number - out of range low",
			input:    "0",
			expected: PublishedPort{},
			error:    "port number out of range: 0",
		},
		{
			name:     "invalid port number - out of range high",
			input:    "65536",
			expected: PublishedPort{},
			error:    "port number out of range: 65536",
		},
		{
			name:     "invalid guest port - non-numeric",
			input:    "8080:abc",
			expected: PublishedPort{},
			error:    "invalid port number 'abc'",
		},
		{
			name:     "invalid IP address",
			input:    "invalid.ip/8080:80",
			expected: PublishedPort{},
			error:    "invalid port publish IP address: invalid.ip",
		},
		{
			name:     "Multiple IP addresses",
			input:    "127.0.0.1/8080:127.0.0.1/8080",
			expected: PublishedPort{},
			error:    "invalid port publish format",
		},
		{
			name:     "too many parts",
			input:    "127.0.0.1/8080:80:443",
			expected: PublishedPort{},
			error:    "invalid port forwarding format",
		},
		{
			name:     "empty string",
			input:    "",
			expected: PublishedPort{},
			error:    "invalid port number",
		},
		{
			name:  "minimum valid port",
			input: "1",
			expected: PublishedPort{
				HostIP:    "127.0.0.1",
				HostPort:  1,
				GuestPort: 1,
			},
			error: "",
		},
		{
			name:  "maximum valid port",
			input: "65535",
			expected: PublishedPort{
				HostIP:    "127.0.0.1",
				HostPort:  65535,
				GuestPort: 65535,
			},
			error: "",
		},
		{
			name:  "IP with single port",
			input: "192.168.1.1/8000",
			expected: PublishedPort{
				HostIP:    "192.168.1.1",
				HostPort:  8000,
				GuestPort: 8000,
			},
			error: "",
		},
		{
			name:  "auto",
			input: "auto",
			expected: PublishedPort{
				Auto: true,
			},
			error: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParsePublishedPort(tt.input)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
			if tt.error != "" {
				return
			}
			if result == nil {
				t.Fatal("expected result but got nil")
			}
			if *result != tt.expected {
				t.Errorf("expected %+v, got %+v", tt.expected, *result)
			}
		})
	}
}

func TestParseHostPort(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected HostPort
		error    string
	}{
		{
			name:  "single port",
			input: "8080",
			expected: HostPort{
				HostPort:  8080,
				GuestPort: 8080,
			},
			error: "",
		},
		{
			name:  "host:guest port mapping",
			input: "3000:8080",
			expected: HostPort{
				HostPort:  3000,
				GuestPort: 8080,
			},
			error: "",
		},
		{
			name:  "auto",
			input: "auto",
			expected: HostPort{
				Auto: true,
			},
			error: "",
		},
		{
			name:     "invalid port number",
			input:    "abc",
			expected: HostPort{},
			error:    "invalid port number 'abc'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseHostPort(tt.input)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
			if tt.error != "" {
				return
			}
			if result == nil {
				t.Fatal("expected result but got nil")
			}
			if *result != tt.expected {
				t.Errorf("expected %+v, got %+v", tt.expected, *result)
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
tcp_published_ports = ["auto"]
tcp_host_ports = ["8080", "3000:3001"]
udp_published_ports = ["5000"]
udp_host_ports = ["12000:1700", "9000"]
`,
			expected: Config{
				Net: Net{
					Mode:              "isolated",
					TCPPublishedPorts: []PublishedPort{{Auto: true}},
					TCPHostPorts:      []HostPort{{HostPort: 8080, GuestPort: 8080}, {HostPort: 3000, GuestPort: 3001}},
					UDPPublishedPorts: []PublishedPort{{HostIP: "127.0.0.1", HostPort: 5000, GuestPort: 5000}},
					UDPHostPorts:      []HostPort{{HostPort: 12000, GuestPort: 1700}, {HostPort: 9000, GuestPort: 9000}},
				},
			},
			error: "",
		},
		{
			name: "empty net config",
			tomlStr: `
[net]
mode = "off"
tcp_published_ports = []
tcp_host_ports = []
udp_published_ports = []
udp_host_ports = []
`,
			expected: Config{
				Net: Net{
					Mode:              "off",
					TCPPublishedPorts: []PublishedPort{},
					TCPHostPorts:      []HostPort{},
					UDPPublishedPorts: []PublishedPort{},
					UDPHostPorts:      []HostPort{},
				},
			},
			error: "",
		},
		{
			name:    "no net section",
			tomlStr: ``,
			expected: Config{
				Net: Net{
					Mode:              "isolated", // default
					TCPPublishedPorts: []PublishedPort{},
					TCPHostPorts:      nil,
					UDPPublishedPorts: []PublishedPort{},
					UDPHostPorts:      nil,
				},
			},
			error: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parse(tt.tomlStr)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
			if tt.error != "" {
				return
			}
			if result == nil {
				t.Fatalf("expected result but got nil")
			}
			expectConfigsEqual(t, result, &tt.expected)
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
blocked_paths = ["/mnt", "/root"]
[environ]
exposed_vars = ["HOME", "PATH", "LC_*"]
set_vars = ["FOO=foobar", "BAR=baz"]
[net]
mode = "isolated"
tcp_published_ports = ["8080", "3000:3001"]
tcp_host_ports = ["auto"]
udp_published_ports = ["5000"]
udp_host_ports = ["12000:1700", "9000"]
`,
			expected: Config{
				Mounts: []Mount{
					{Source: "/home/user/docs", Target: "/home/user/docs"},
					{Source: "/tmp", Target: "~/tmp", RW: true, Overlay: true},
					{Source: "/home/user/work", Target: "/home/user/work", RW: true},
					{Source: "/media", Target: "~/media", RW: true},
				},
				BlockedPaths: []string{"/mnt", "/root"},
				Environ: Environ{
					ExposedVars: []string{"HOME", "PATH", "LC_*"},
					SetVars: []EnvVar{
						{Name: "FOO", Value: "foobar"},
						{Name: "BAR", Value: "baz"},
					},
				},
				Net: Net{
					Mode: "isolated",
					TCPPublishedPorts: []PublishedPort{
						{HostIP: "127.0.0.1", HostPort: 8080, GuestPort: 8080},
						{HostIP: "127.0.0.1", HostPort: 3000, GuestPort: 3001},
					},
					TCPHostPorts:      []HostPort{{Auto: true}},
					UDPPublishedPorts: []PublishedPort{{HostIP: "127.0.0.1", HostPort: 5000, GuestPort: 5000}},
					UDPHostPorts:      []HostPort{{HostPort: 12000, GuestPort: 1700}, {HostPort: 9000, GuestPort: 9000}},
				},
			},
			error: "",
		},
		{
			name:    "empty config",
			tomlStr: ``,
			expected: Config{
				Mounts:       nil,
				BlockedPaths: nil,
				Net: Net{
					Mode:              "isolated", // default
					TCPPublishedPorts: []PublishedPort{},
					TCPHostPorts:      nil,
					UDPPublishedPorts: []PublishedPort{},
					UDPHostPorts:      nil,
				},
			},
			error: "",
		},
		{
			name: "invalid exposed_vars pattern",
			tomlStr: `
environ.exposed_vars = ["HOME", "INVALID["]
`,
			expected: Config{},
			error:    "invalid exposed_env_vars pattern 'INVALID['",
		},
		{
			name: "valid set_vars",
			tomlStr: `
environ.set_vars = ["FOO=bar", "BAZ=qux=123"]
`,
			expected: Config{
				Environ: Environ{
					SetVars: []EnvVar{
						{Name: "FOO", Value: "bar"},
						{Name: "BAZ", Value: "qux=123"},
					},
				},
				Net: Net{
					Mode: "isolated",
				},
			},
			error: "",
		},
		{
			name: "invalid set_vars - missing equals",
			tomlStr: `
environ.set_vars = ["FOO"]
`,
			expected: Config{},
			error:    "environment variable should have a name=value form",
		},
		{
			name: "invalid set_vars - empty name",
			tomlStr: `
environ.set_vars = ["=value"]
`,
			expected: Config{},
			error:    "environment variable name should not be empty",
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
			name: "invalid tcp_published_ports, not a string",
			tomlStr: `
[net]
tcp_published_ports = [8080]
`,
			expected: Config{},
			error:    "published port entry should be a string",
		},
		{
			name: "invalid tcp_published_ports",
			tomlStr: `
[net]
tcp_published_ports = ["8080", "invalid_port"]
`,
			expected: Config{},
			error:    "invalid port number 'invalid_port'",
		},
		{
			name: "invalid tcp_host_ports, not a string",
			tomlStr: `
[net]
tcp_host_ports = [8080]
`,
			expected: Config{},
			error:    "host port entry should be a string",
		},
		{
			name: "invalid tcp_published_ports, auto not the only option",
			tomlStr: `
[net]
tcp_published_ports = ["8080", "auto"]
`,
			expected: Config{},
			error:    "invalid tcp_published_ports: \"auto\" must be the only published port entry",
		},
		{
			name: "invalid udp_published_ports, auto not the only option",
			tomlStr: `
[net]
udp_published_ports = ["8080", "auto"]
`,
			expected: Config{},
			error:    "invalid udp_published_ports: \"auto\" must be the only published port entry",
		},
		{
			name: "invalid tcp_host_ports, auto not the only option",
			tomlStr: `
[net]
tcp_host_ports = ["8080", "auto"]
`,
			expected: Config{},
			error:    "invalid tcp_host_ports: \"auto\" must be the only host port entry",
		},
		{
			name: "invalid udp_host_ports, auto not the only option",
			tomlStr: `
[net]
udp_host_ports = ["8080", "auto"]
`,
			expected: Config{},
			error:    "invalid udp_host_ports: \"auto\" must be the only host port entry",
		},
		{
			name: "invalid udp_published_ports",
			tomlStr: `
[net]
udp_published_ports = ["0"]
`,
			expected: Config{},
			error:    "port number out of range: 0",
		},
		{
			name: "invalid udp_host_ports",
			tomlStr: `
[net]
udp_host_ports = ["abc"]
`,
			expected: Config{},
			error:    "invalid port number 'abc'",
		},
		{
			name: "invalid net mode",
			tomlStr: `
[net]
mode = "foo"
`,
			expected: Config{},
			error:    "invalid network mode 'foo': must be 'off' or 'isolated'",
		},
		{
			name: "invalid mount format",
			tomlStr: `
mounts = ["/tmp:/tmp2:/tmp3:rw"]
`,
			expected: Config{},
			error:    "mount config has too many parts separated by ':'",
		},
		{
			name: "invalid mounts, not normalized path",
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
		{
			name: "invalid blocked path",
			tomlStr: `
blocked_paths = ["foo"]
`,
			expected: Config{},
			error:    "invalid blocked_paths 'foo': path must start with / or ~/",
		},
		{
			name: "unrecognized key",
			tomlStr: `
mount = []
`,
			expected: Config{},
			error:    "unrecognized key: mount",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parse(tt.tomlStr)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
			if tt.error != "" {
				return
			}

			if result == nil {
				t.Fatalf("expected result but got nil")
			}

			expectConfigsEqual(t, result, &tt.expected)
		})
	}
}

func TestValidateNetworkMode(t *testing.T) {
	validModes := []string{"off", "isolated", "unjailed"}
	for _, mode := range validModes {
		if err := validateNetworkMode(mode); err != nil {
			t.Errorf("expected no error for mode '%s', got: %v", mode, err)
		}
	}

	invalidModes := []string{"invalid", "", "OFF"}
	for _, mode := range invalidModes {
		err := validateNetworkMode(mode)
		if err == nil {
			t.Errorf("expected error for mode '%s'", mode)
		} else if !strings.Contains(err.Error(), "invalid network mode") {
			t.Errorf("unexpected error for mode '%s': %s", mode, err.Error())
		}
	}
}

func TestParseMountCompact(t *testing.T) {
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
			result, err := ParseMountCompact(tt.input)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
			if tt.error != "" {
				return
			}

			if result == nil {
				t.Fatalf("expected result but got nil")
			}
			if *result != tt.expected {
				t.Fatalf("expected %+v, got %+v", tt.expected, *result)
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
			result, err := parse(tt.tomlStr)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
			if tt.error != "" {
				return
			}
			expectSlicesEqual(t, "Mounts", result.Mounts, tt.expected)
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
			err := validatePaths("test_prop", tt.paths, osutil.ValidateRootOrHomeSubPath)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
		})
	}
}

func TestValidateExposedEnvVars(t *testing.T) {
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
			error:    "invalid exposed_env_vars pattern 'LC_['",
		},
		{
			name:     "invalid pattern - unclosed bracket at end",
			patterns: []string{"HOME", "PATH["},
			error:    "invalid exposed_env_vars pattern 'PATH['",
		},
		{
			name:     "multiple invalid patterns",
			patterns: []string{"HOME", "LC_[", "VALID_*", "BAD["},
			error:    "invalid exposed_env_vars pattern 'LC_['",
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
			error:    "invalid exposed_env_vars pattern 'INVALID['",
		},
		{
			name:     "valid edge case patterns",
			patterns: []string{"*", "?", "[a-z]", "[0-9]*", "VAR_[!0-9]"},
			error:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvironExposedVars(tt.patterns)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
		})
	}
}

func TestEnvVarUnmarshalTOML(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected EnvVar
		error    string
	}{
		{
			name:     "valid name=value",
			input:    "FOO=bar",
			expected: EnvVar{Name: "FOO", Value: "bar"},
			error:    "",
		},
		{
			name:     "empty value",
			input:    "FOO=",
			expected: EnvVar{Name: "FOO", Value: ""},
			error:    "",
		},
		{
			name:     "value with equals sign",
			input:    "FOO=bar=baz",
			expected: EnvVar{Name: "FOO", Value: "bar=baz"},
			error:    "",
		},
		{
			name:     "name with spaces trimmed",
			input:    "  FOO  =bar",
			expected: EnvVar{Name: "FOO", Value: "bar"},
			error:    "",
		},
		{
			name:     "missing equals sign",
			input:    "FOO",
			expected: EnvVar{},
			error:    "environment variable should have a name=value form",
		},
		{
			name:     "empty name",
			input:    "=bar",
			expected: EnvVar{},
			error:    "environment variable name should not be empty",
		},
		{
			name:     "whitespace-only name",
			input:    "   =bar",
			expected: EnvVar{},
			error:    "environment variable name should not be empty",
		},
		{
			name:     "wrong type",
			input:    123,
			expected: EnvVar{},
			error:    "environment variable should be a string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var e EnvVar
			err := e.UnmarshalTOML(tt.input)
			if terr := checkError(tt.error, err); terr != nil {
				t.Fatal(terr)
			}
			if tt.error == "" {
				if e.Name != tt.expected.Name {
					t.Errorf("expected Name '%s', got '%s'", tt.expected.Name, e.Name)
				}
				if e.Value != tt.expected.Value {
					t.Errorf("expected Value '%s', got '%s'", tt.expected.Value, e.Value)
				}
			}
		})
	}
}

func TestEnvVarExpand(t *testing.T) {
	tests := []struct {
		name     string
		envVar   EnvVar
		mapping  func(string) string
		expected string
	}{
		{
			name:     "no expansion needed",
			envVar:   EnvVar{Name: "FOO", Value: "bar"},
			mapping:  func(s string) string { return "" },
			expected: "FOO=bar",
		},
		{
			name:     "expand variable",
			envVar:   EnvVar{Name: "PATH", Value: "/home/$USER/bin"},
			mapping:  func(s string) string { return "alice" },
			expected: "PATH=/home/alice/bin",
		},
		{
			name:     "empty value",
			envVar:   EnvVar{Name: "EMPTY", Value: ""},
			mapping:  func(s string) string { return "" },
			expected: "EMPTY=",
		},
		{
			name:     "expand with braces syntax",
			envVar:   EnvVar{Name: "MSG", Value: "Hello ${NAME}!"},
			mapping:  func(s string) string { return "World" },
			expected: "MSG=Hello World!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.envVar.Expand(tt.mapping)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}
