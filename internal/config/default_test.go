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

	if cfg.Environ.ExposedVars == nil {
		t.Errorf("Expected environ.exposed_vars to be not nil")
	} else if l := len(cfg.Environ.ExposedVars); l < 10 {
		t.Errorf("Expected environ.exposed_vars to have at least 10 elements, got %d", l)
	}

	expectedSetVars := []EnvVar{
		{Name: "debian_chroot", Value: "drop"},
		{Name: "PATH", Value: "${PATH}:${HOME}/.local-host/bin"},
	}
	if !slices.Equal(cfg.Environ.SetVars, expectedSetVars) {
		t.Errorf("Expected environ.set_vars to be %+v, got %+v", expectedSetVars, cfg.Environ.SetVars)
	}

	net := cfg.Net
	if net.Mode != "isolated" {
		t.Errorf("Expected default net mode 'isolated', got %s", net.Mode)
	}

	expectEmptyList(t, "tcp_published_ports", net.TCPPublishedPorts)
	expectEmptyList(t, "tcp_host_ports", net.TCPHostPorts)
	expectEmptyList(t, "udp_published_ports", net.UDPPublishedPorts)
	expectEmptyList(t, "udp_host_ports", net.UDPHostPorts)
}

func TestWriteDefaultForEnv(t *testing.T) {
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "base.toml")
	envPath := filepath.Join(tempDir, "env.toml")

	projectDir := filepath.Join(tempDir, "project")
	gitDir := filepath.Join(projectDir, ".git")
	if err := os.MkdirAll(gitDir, 0700); err != nil {
		t.Fatalf("Failed to create test dirs: %v", err)
	}

	mounts := []DefaultMount{
		{Entry: projectDir + "::rw"},
		{Entry: gitDir},
	}

	// WriteDefaultForEnv generates a config that extends base.toml.
	if err := WriteBase(basePath, tempDir); err != nil {
		t.Fatalf("WriteDefault failed: %v", err)
	}
	if err := WriteDefaultForEnv(envPath, mounts, tempDir); err != nil {
		t.Fatalf("WriteDefaultForEnv failed: %v", err)
	}

	cfg, err := Read(envPath, "/test-home-dir/")
	if err != nil {
		t.Fatalf("Failed to read created env config: %v", err)
	}

	if cfg.Extends != "./base.toml" {
		t.Errorf("Expected extends './base.toml', got %q", cfg.Extends)
	}

	expectedMounts := []Mount{
		{Source: projectDir, Target: projectDir, RW: true},
		{Source: gitDir, Target: gitDir},
	}
	if !slices.Equal(cfg.Mounts, expectedMounts) {
		t.Errorf("Expected mounts %+v, got %+v", expectedMounts, cfg.Mounts)
	}

	// Net mode should default to "isolated" (from base config).
	if cfg.Net.Mode != "isolated" {
		t.Errorf("Expected net mode 'isolated', got %s", cfg.Net.Mode)
	}

	expectEmptyList(t, "tcp_published_ports", cfg.Net.TCPPublishedPorts)
	expectEmptyList(t, "tcp_host_ports", cfg.Net.TCPHostPorts)
	expectEmptyList(t, "udp_published_ports", cfg.Net.UDPPublishedPorts)
	expectEmptyList(t, "udp_host_ports", cfg.Net.UDPHostPorts)
}

func TestFilterExistingEntries(t *testing.T) {
	entries := []DefaultMount{
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

	if len(filtered) != 1 {
		t.Fatalf("Expected 1 filtered entry, got %d", len(filtered))
	}

	if filtered[0].Entry != "~/bar" {
		t.Errorf("Invalid filtered entry '%s'", filtered[0].Entry)
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
