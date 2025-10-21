package config

import (
	"os"
	"path/filepath"
	"testing"
)

func expectEmptyList(t *testing.T, name string, l []string) {
	t.Helper()
	if l == nil {
		t.Errorf("Expected %s to be not nil", name)
	} else if len(l) != 0 {
		t.Errorf("Expected %s to be empty, got %v", name, l)
	}
}

func TestWriteDefault(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config")

	err := WriteDefault(configPath, tempDir)
	if err != nil {
		t.Fatalf("WriteDefault failed: %v", err)
	}

	cfg, err := Read(configPath)
	if err != nil {
		t.Fatalf("Failed to read created default config: %v", err)
	}

	if cfg.PathsRO == nil {
		t.Errorf("Expected paths_ro to be not nil")
	}
	expectEmptyList(t, "home_writeable", cfg.HomeWriteable)
	if cfg.Blocked == nil {
		t.Errorf("Expected blocked to be not nil")
	}

	if cfg.EnvExpose == nil {
		t.Errorf("Expected env_expose to be not nil")
	} else if len(cfg.EnvExpose) < 10 {
		t.Errorf("Expected env_expose to have at least 10 elements, got %d", len(cfg.EnvExpose))
	}

	net := cfg.Net
	if net.Mode != "isolated" {
		t.Errorf("Expected default net mode 'isolated', got %s", net.Mode)
	}

	if len(net.TCPPortsToHost) != 1 || net.TCPPortsToHost[0] != "auto" {
		t.Errorf("Expected tcp_ports_to_host to be [\"auto\"], got %v", net.TCPPortsToHost)
	}

	expectEmptyList(t, "tcp_ports_from_host", net.TCPPortsFromHost)
	expectEmptyList(t, "udp_ports_to_host", net.UDPPortsToHost)
	expectEmptyList(t, "udp_ports_from_host", net.UDPPortsFromHost)
}

func TestFilterExistingEntries(t *testing.T) {
	entries := []configFileEntry{
		{"foo", ""},
		{"bar", ""},
		{"baz", ""},
	}

	tempDir := t.TempDir()

	testFile1 := filepath.Join(tempDir, "bar")
	if err := os.WriteFile(testFile1, []byte(""), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	filtered := keepExistingEntries(tempDir, entries)

	if len(filtered) != 1 {
		t.Fatalf("Expected 1 filtered entry, got %d", len(filtered))
	}

	if filtered[0].path != "bar" {
		t.Errorf("Invalid filtered entry '%s'", filtered[0].path)
	}
}

func TestToTomlString(t *testing.T) {
	tests := []struct {
		name     string
		entries  []configFileEntry
		expected string
	}{
		{
			name:     "empty",
			entries:  []configFileEntry{},
			expected: "[]",
		},
		{
			name: "single entry without comment",
			entries: []configFileEntry{
				{".bashrc", ""},
			},
			expected: `[
  ".bashrc",
]`,
		},
		{
			name: "single entry with comment",
			entries: []configFileEntry{
				{".gitconfig", " # comment foo"},
			},
			expected: `[
  ".gitconfig", # comment foo
]`,
		},
		{
			name: "multiple entries",
			entries: []configFileEntry{
				{".bashrc", ""},
				{".gitconfig", " # comment bar"},
			},
			expected: `[
  ".bashrc",
  ".gitconfig", # comment bar
]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toTomlString(tt.entries)
			if result != tt.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tt.expected, result)
			}
		})
	}
}
