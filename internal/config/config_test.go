package config

import (
	"strings"
	"testing"
)

// expectListEquals compares two string slices and reports detailed error if they differ
func expectListEquals(t *testing.T, fieldName string, actual, expected []string) {
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
					t.Errorf("expected error")
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
tcp_ports_to_host = ["auto"]
tcp_ports_from_host = ["8080", "3000:3001"]
udp_ports_to_host = ["5000"]
udp_ports_from_host = ["192.168.1.1/12000:1700", "9000"]
`,
			expected: Config{
				Net: Net{
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
tcp_ports_to_host = []
tcp_ports_from_host = []
udp_ports_to_host = []
udp_ports_from_host = []
`,
			expected: Config{
				Net: Net{
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
					t.Errorf("expected error")
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
			expectListEquals(t, "TCPPortsToHost", got.TCPPortsToHost, expected.TCPPortsToHost)
			expectListEquals(t, "TCPPortsFromHost", got.TCPPortsFromHost, expected.TCPPortsFromHost)
			expectListEquals(t, "UDPPortsToHost", got.UDPPortsToHost, expected.UDPPortsToHost)
			expectListEquals(t, "UDPPortsFromHost", got.UDPPortsFromHost, expected.UDPPortsFromHost)
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
home_visible = ["/home/user/docs", "/tmp"]
home_writeable = ["/home/user/work"]
hide = ["/etc/shadow", "/root"]
env_expose = ["HOME", "PATH", "LC_*"]

[net]
tcp_ports_to_host = ["8080", "3000:3001"]
tcp_ports_from_host = ["auto"]
udp_ports_to_host = ["5000"]
udp_ports_from_host = ["192.168.1.1/12000:1700", "9000"]
`,
			expected: Config{
				HomeVisible:   []string{"/home/user/docs", "/tmp"},
				HomeWriteable: []string{"/home/user/work"},
				Hide:          []string{"/etc/shadow", "/root"},
				EnvExpose:     []string{"HOME", "PATH", "LC_*"},
				Net: Net{
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
				HomeVisible:   nil,
				HomeWriteable: nil,
				Hide:          nil,
				EnvExpose:     nil,
				Net: Net{
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Parse(tt.tomlStr)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error")
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

			expectListEquals(t, "HomeVisible", result.HomeVisible, tt.expected.HomeVisible)
			expectListEquals(t, "HomeWriteable", result.HomeWriteable, tt.expected.HomeWriteable)
			expectListEquals(t, "Hide", result.Hide, tt.expected.Hide)
			expectListEquals(t, "EnvExpose", result.EnvExpose, tt.expected.EnvExpose)

			expectListEquals(t, "Net.TCPPortsToHost", result.Net.TCPPortsToHost, tt.expected.Net.TCPPortsToHost)
			expectListEquals(t, "Net.TCPPortsFromHost", result.Net.TCPPortsFromHost, tt.expected.Net.TCPPortsFromHost)
			expectListEquals(t, "Net.UDPPortsToHost", result.Net.UDPPortsToHost, tt.expected.Net.UDPPortsToHost)
			expectListEquals(t, "Net.UDPPortsFromHost", result.Net.UDPPortsFromHost, tt.expected.Net.UDPPortsFromHost)
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
					t.Errorf("expected error '%s' but got none", tt.error)
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
