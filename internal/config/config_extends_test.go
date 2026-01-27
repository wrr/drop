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

// Test cases for 'extends' config key that allows to combine multiple
// config files.

package config

import (
	"fmt"
	"testing"
)

func fakeReadFile(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("file not found: %s", path)
		}
		return []byte(content), nil
	}
}

func read(path string, files map[string]string) (*Config, error) {
	r := &reader{
		files:    make(map[string]bool),
		homeDir:  "/home/alice",
		readFile: fakeReadFile(files),
	}
	return r.read(path)
}

func TestExtends(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		expected Config
		error    string
	}{
		{
			name: "relative path extends",
			files: map[string]string{
				"/config/entry.toml": `
extends = "base.toml"
mounts = ["~/entry-mount"]
net.mode = "off"
`,
				"/config/base.toml": `
mounts = ["~/base-mount"]
net.mode = "isolated"
`,
			},
			expected: Config{
				Extends: "base.toml",
				Mounts: []Mount{
					{Source: "~/base-mount", Target: "~/base-mount"},
					{Source: "~/entry-mount", Target: "~/entry-mount"},
				},
				Net: Net{Mode: "off"},
			},
		},
		{
			name: "home path extends",
			files: map[string]string{
				"/config/entry.toml": `
extends = "~/base.toml"
mounts = ["~/entry-mount"]
`,
				"/home/alice/base.toml": `
mounts = ["~/base-mount"]
net.mode = "off"
`,
			},
			expected: Config{
				Extends: "~/base.toml",
				Mounts: []Mount{
					{Source: "~/base-mount", Target: "~/base-mount"},
					{Source: "~/entry-mount", Target: "~/entry-mount"},
				},
				Net: Net{Mode: "off"},
			},
		},
		{
			name: "absolute root path extends",
			files: map[string]string{
				"/config/entry.toml": `
extends = "/etc/drop/base.toml"
mounts = ["~/entry-mount"]
`,
				"/etc/drop/base.toml": `
mounts = ["~/base-mount"]
`,
			},
			expected: Config{
				Extends: "/etc/drop/base.toml",
				Mounts: []Mount{
					{Source: "~/base-mount", Target: "~/base-mount"},
					{Source: "~/entry-mount", Target: "~/entry-mount"},
				},
				Net: Net{Mode: "isolated"},
			},
		},
		{
			name: "multi-level extends",
			files: map[string]string{
				"/config/entry.toml": `
extends = "base.toml"
mounts = ["~/entry-mount"]
blocked_paths = ["/child-blocked"]
`,
				"/config/base.toml": `
extends = "base-base.toml"
mounts = ["~/base-mount"]
blocked_paths = ["/base-blocked"]
`,
				"/config/base-base.toml": `
mounts = ["~/base-base-mount"]
blocked_paths = ["/base-base-blocked"]
`,
			},
			expected: Config{
				Extends: "base.toml",
				Mounts: []Mount{
					{Source: "~/base-base-mount", Target: "~/base-base-mount"},
					{Source: "~/base-mount", Target: "~/base-mount"},
					{Source: "~/entry-mount", Target: "~/entry-mount"},
				},
				BlockedPaths: []string{"/base-base-blocked", "/base-blocked", "/child-blocked"},
				Net:          Net{Mode: "isolated"},
			},
		},
		{
			name: "all fields merge",
			files: map[string]string{
				"/config/entry.toml": `
extends = "base.toml"
mounts = ["~/entry-mount"]
blocked_paths = ["/entry-blocked"]
[cwd]
mounts = ["entry-cwd-mount"]
blocked_paths = ["entry-cwd-blocked"]
[environ]
exposed_vars = ["ENTRY_VAR"]
set_vars = ["ENTRY_SET=entry_value"]
[net]
tcp_published_ports = ["9000"]
tcp_host_ports = ["9001"]
udp_published_ports = ["9002"]
udp_host_ports = ["9003"]
`,
				"/config/base.toml": `
mounts = ["~/base-mount"]
blocked_paths = ["/base-blocked"]
[cwd]
mounts = ["base-cwd-mount"]
blocked_paths = ["base-cwd-blocked"]
[environ]
exposed_vars = ["BASE_VAR"]
set_vars = ["BASE_SET=base_value"]
[net]
tcp_published_ports = ["8000"]
tcp_host_ports = ["8001"]
udp_published_ports = ["8002"]
udp_host_ports = ["8003"]
`,
			},
			expected: Config{
				Extends: "base.toml",
				Mounts: []Mount{
					{Source: "~/base-mount", Target: "~/base-mount"},
					{Source: "~/entry-mount", Target: "~/entry-mount"},
				},
				BlockedPaths: []string{"/base-blocked", "/entry-blocked"},
				Cwd: Cwd{
					Mounts: []Mount{
						{Source: "base-cwd-mount", Target: "base-cwd-mount"},
						{Source: "entry-cwd-mount", Target: "entry-cwd-mount"},
					},
					BlockedPaths: []string{"base-cwd-blocked", "entry-cwd-blocked"},
				},
				Environ: Environ{
					ExposedVars: []string{"BASE_VAR", "ENTRY_VAR"},
					SetVars: []EnvVar{
						{Name: "BASE_SET", Value: "base_value"},
						{Name: "ENTRY_SET", Value: "entry_value"},
					},
				},
				Net: Net{
					Mode: "isolated",
					TCPPublishedPorts: []PublishedPort{
						{HostIP: "127.0.0.1", HostPort: 8000, GuestPort: 8000},
						{HostIP: "127.0.0.1", HostPort: 9000, GuestPort: 9000},
					},
					TCPHostPorts: []HostPort{
						{HostPort: 8001, GuestPort: 8001},
						{HostPort: 9001, GuestPort: 9001},
					},
					UDPPublishedPorts: []PublishedPort{
						{HostIP: "127.0.0.1", HostPort: 8002, GuestPort: 8002},
						{HostIP: "127.0.0.1", HostPort: 9002, GuestPort: 9002},
					},
					UDPHostPorts: []HostPort{
						{HostPort: 8003, GuestPort: 8003},
						{HostPort: 9003, GuestPort: 9003},
					},
				},
			},
		},
		{
			name: "circular extends",
			files: map[string]string{
				"/config/entry.toml": `
extends = "b.toml"
`,
				"/config/b.toml": `
extends = "entry.toml"
`,
			},
			error: "circular 'extends': /config/entry.toml already included",
		},
		{
			name: "circular extends: self-reference",
			files: map[string]string{
				"/config/entry.toml": `
extends = "entry.toml"
`,
			},
			error: "circular 'extends': /config/entry.toml already included",
		},
		{
			name: "missing base",
			files: map[string]string{
				"/config/entry.toml": `
extends = "missing.toml"
`,
			},
			error: "file not found: /config/missing.toml",
		},
		{
			name: "not normalized path",
			files: map[string]string{
				"/config/entry.toml": `
extends = "../etc/passwd"
`,
			},
			error: "extends path ../etc/passwd invalid: path is not normalized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := read("/config/entry.toml", tt.files)
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
