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
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func expectEmptyList[T any](t *testing.T, name string, l []T) {
	t.Helper()
	if len(l) != 0 {
		t.Errorf("Expected %s to be empty, got %v", name, l)
	}
}

func TestWriteBase(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config")

	err := WriteBase(configPath, tempDir)
	if err != nil {
		t.Fatalf("WriteBase failed: %v", err)
	}

	cfg, err := Read(configPath, "/test-home-dir/")
	if err != nil {
		t.Fatalf("Failed to read created base config: %v", err)
	}

	if cfg.Mounts == nil {
		t.Errorf("Expected mounts to be not nil")
	}

	if cfg.Runtime != RuntimeNative {
		t.Errorf("Expected default runtime %q, got %q", RuntimeNative, cfg.Runtime)
	}

	if cfg.Environ.ExposedVars == nil {
		t.Errorf("Expected environ.exposed_vars to be not nil")
	} else if l := len(cfg.Environ.ExposedVars); l < 10 {
		t.Errorf("Expected environ.exposed_vars to have at least 10 elements, got %d", l)
	}

	expectedSetVars := []EnvVar{
		{Name: "debian_chroot", Value: "drop"},
		{Name: "PATH", Value: "${PATH}:${HOME}/.local-host/bin"},
		{Name: "GOBIN", Value: "${HOME}/.local/bin"},
		{Name: "CARGO_INSTALL_ROOT", Value: "${HOME}/.local"},
		{Name: "UV_TOOL_DIR", Value: "${HOME}/.local/share/uv-drop/tools"},
		{Name: "UV_PYTHON_INSTALL_DIR", Value: "${HOME}/.local/share/uv-drop/python"},
		{Name: "UV_TOOL_BIN_DIR", Value: "${HOME}/.local/bin"},
		{Name: "PIPX_HOME", Value: "${HOME}/.local/share/pipx-drop"},
	}
	if !slices.Equal(cfg.Environ.SetVars, expectedSetVars) {
		t.Errorf("Expected environ.set_vars to be %+v, got %+v", expectedSetVars, cfg.Environ.SetVars)
	}

	expectedBlockedPaths := []string{"/etc/ssh/ssh_config.d"}
	if !slices.Equal(cfg.BlockedPaths, expectedBlockedPaths) {
		t.Errorf("Expected blocked_paths to be %v, got %v", expectedBlockedPaths, cfg.BlockedPaths)
	}

	net := cfg.Net
	if net.Mode != NetModeIsolated {
		t.Errorf("Expected default net mode 'isolated', got %s", net.Mode)
	}

	expectEmptyList(t, "tcp_published_ports", net.TCPPublishedPorts)
	expectEmptyList(t, "tcp_host_ports", net.TCPHostPorts)
	expectEmptyList(t, "udp_published_ports", net.UDPPublishedPorts)
	expectEmptyList(t, "udp_host_ports", net.UDPHostPorts)
}

func TestWriteDefaultForEnv(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home-dir")
	basePath := filepath.Join(tempDir, "base.toml")
	envPath := filepath.Join(tempDir, "env.toml")

	projectDir := filepath.Join(homeDir, "project")
	gitDir := filepath.Join(projectDir, ".git")
	if err := os.MkdirAll(gitDir, 0700); err != nil {
		t.Fatalf("Failed to create test dirs: %v", err)
	}

	// WriteDefaultForEnv generates a config that extends base.toml.
	if err := WriteBase(basePath, homeDir); err != nil {
		t.Fatalf("WriteBase failed: %v", err)
	}
	if err := WriteDefaultForEnv(envPath, false, homeDir, projectDir); err != nil {
		t.Fatalf("WriteDefaultForEnv failed: %v", err)
	}

	cfg, err := Read(envPath, homeDir)
	if err != nil {
		t.Fatalf("Failed to read created env config: %v", err)
	}

	if cfg.Extends != "./base.toml" {
		t.Errorf("Expected extends './base.toml', got %q", cfg.Extends)
	}

	expectedMounts := []Mount{
		{Source: "~/project", Target: "~/project", RW: true},
		{Source: "~/project/.git", Target: "~/project/.git"},
	}
	if !slices.Equal(cfg.Mounts, expectedMounts) {
		t.Errorf("Expected mounts %+v, got %+v", expectedMounts, cfg.Mounts)
	}

	// Net mode should default to "isolated" (from base config).
	if cfg.Net.Mode != NetModeIsolated {
		t.Errorf("Expected net mode 'isolated', got %s", cfg.Net.Mode)
	}

	expectEmptyList(t, "tcp_published_ports", cfg.Net.TCPPublishedPorts)
	expectEmptyList(t, "tcp_host_ports", cfg.Net.TCPHostPorts)
	expectEmptyList(t, "udp_published_ports", cfg.Net.UDPPublishedPorts)
	expectEmptyList(t, "udp_host_ports", cfg.Net.UDPHostPorts)
}

func TestCwdMounts(t *testing.T) {
	homeDir := "/home/alice"
	tests := []struct {
		name     string
		noCwd    bool
		cwd      string
		expected []string
	}{
		{
			name:     "cwd under home",
			cwd:      "/home/alice/project",
			expected: []string{"~/project::rw", "~/project/.git"},
		},
		{
			name:     "cwd outside home",
			cwd:      "/srv/project",
			expected: []string{"/srv/project::rw", "/srv/project/.git"},
		},
		{
			name: "cwd is home",
			cwd:  "/home/alice",
		},
		{
			name: "cwd is parent of home",
			cwd:  "/home",
		},
		{
			name:  "noCwd",
			noCwd: true,
			cwd:   "/home/alice/project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var entries []string
			for _, m := range cwdMounts(tt.noCwd, homeDir, tt.cwd) {
				entries = append(entries, m.Entry)
			}
			if !slices.Equal(entries, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, entries)
			}
		})
	}
}

func TestFilterExistingEntries(t *testing.T) {
	entries := []DefaultMount{
		header("comment foo"),
		{Entry: "~/foo"},
		{Entry: "~/bar"},
		{Entry: "~/baz:~/baz-host"},
	}

	homeDir := t.TempDir()

	testFile1 := filepath.Join(homeDir, "bar")
	if err := os.WriteFile(testFile1, []byte(""), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	filtered := keepExistingEntries(entries, homeDir)

	expected := []DefaultMount{header("comment foo"), {Entry: "~/bar"}}
	if !slices.Equal(filtered, expected) {
		t.Errorf("Expected filtered entries %+v, got %+v", expected, filtered)
	}
}

func TestMountEntriesToToml(t *testing.T) {
	tests := []struct {
		name     string
		entries  []DefaultMount
		expected string
	}{
		{
			name:     "empty",
			entries:  []DefaultMount{},
			expected: "[]",
		},
		{
			name: "single entry without comment",
			entries: []DefaultMount{
				{Entry: "~/.bashrc"},
			},
			expected: `[
  "~/.bashrc",
]`,
		},
		{
			name: "single entry with comment",
			entries: []DefaultMount{
				{Entry: "~/.gitconfig", Comment: "comment foo"},
			},
			expected: `[
  "~/.gitconfig", # comment foo
]`,
		},
		{
			name: "multiple entries",
			entries: []DefaultMount{
				{Entry: "~/.bashrc:~/.bashrc-host"},
				{Entry: "~/.gitconfig", Comment: "comment bar"},
			},
			expected: `[
  "~/.bashrc:~/.bashrc-host",
  "~/.gitconfig", # comment bar
]`,
		},
		{
			name: "header followed by entries",
			entries: []DefaultMount{
				{Entry: "~/.bashrc"},
				header("comment foo"),
				{Entry: "~/.gitconfig", Comment: "comment bar"},
			},
			expected: `[
  "~/.bashrc",

  # comment foo
  "~/.gitconfig", # comment bar
]`,
		},
		{
			name: "headers not followed by entries",
			entries: []DefaultMount{
				header("comment foo"),
				header("comment bar"),
				{Entry: "~/.bashrc"},
				header("comment baz"),
			},
			expected: `[
  # comment bar
  "~/.bashrc",
]`,
		},
		{
			name: "headers only",
			entries: []DefaultMount{
				header("comment foo"),
			},
			expected: "[]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mountEntriesToToml(tt.entries)
			if result != tt.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tt.expected, result)
			}
		})
	}
}
