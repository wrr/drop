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

func TestParsePortForward(t *testing.T) {
	tests := []struct {
		name     string
		portSpec string
		expected *PortForward
		error    string
	}{
		{
			name:     "single port",
			portSpec: "8080",
			expected: &PortForward{
				HostIP:    "0.0.0.0",
				HostPort:  8080,
				GuestPort: 8080,
			},
			error: "",
		},
		{
			name:     "host:guest port mapping",
			portSpec: "8080:80",
			expected: &PortForward{
				HostIP:    "0.0.0.0",
				HostPort:  8080,
				GuestPort: 80,
			},
			error: "",
		},
		{
			name:     "IP:host:guest port mapping",
			portSpec: "127.0.0.1:8080:80",
			expected: &PortForward{
				HostIP:    "127.0.0.1",
				HostPort:  8080,
				GuestPort: 80,
			},
			error: "",
		},
		{
			name:     "invalid port number - non-numeric",
			portSpec: "abc",
			expected: nil,
			error:    "invalid port number 'abc'",
		},
		{
			name:     "invalid port number - out of range low",
			portSpec: "0",
			expected: nil,
			error:    "port number out of range: 0",
		},
		{
			name:     "invalid port number - out of range high",
			portSpec: "65536",
			expected: nil,
			error:    "port number out of range: 65536",
		},
		{
			name:     "invalid guest port - non-numeric",
			portSpec: "8080:abc",
			expected: nil,
			error:    "invalid port number 'abc'",
		},
		{
			name:     "invalid IP address",
			portSpec: "invalid.ip:8080:80",
			expected: nil,
			error:    "invalid port forwarding IP address: invalid.ip",
		},
		{
			name:     "too many parts",
			portSpec: "127.0.0.1:8080:80:443",
			expected: nil,
			error:    "invalid port forwarding format",
		},
		{
			name:     "empty string",
			portSpec: "",
			expected: nil,
			error:    "invalid port number",
		},
		{
			name:     "minimum valid port",
			portSpec: "1",
			expected: &PortForward{
				HostIP:    "0.0.0.0",
				HostPort:  1,
				GuestPort: 1,
			},
			error: "",
		},
		{
			name:     "maximum valid port",
			portSpec: "65535",
			expected: &PortForward{
				HostIP:    "0.0.0.0",
				HostPort:  65535,
				GuestPort: 65535,
			},
			error: "",
		},
		{
			name:     "IP with single port",
			portSpec: "192.168.1.1:8000",
			expected: &PortForward{
				HostIP:    "192.168.1.1",
				HostPort:  8000,
				GuestPort: 8000,
			},
			error: "",
		},
		{
			name:     "Three ports",
			portSpec: "124:200:8000",
			expected: nil,
			error:    "invalid port forwarding format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParsePortForward(tt.portSpec)

			if tt.error != "" {
				if err == nil {
					t.Errorf("expected error containing '%s' but got none", tt.error)
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

			if result.HostIP != tt.expected.HostIP {
				t.Errorf("expected HostIP %s, got %s", tt.expected.HostIP, result.HostIP)
			}

			if result.HostPort != tt.expected.HostPort {
				t.Errorf("expected HostPort %d, got %d", tt.expected.HostPort, result.HostPort)
			}

			if result.GuestPort != tt.expected.GuestPort {
				t.Errorf("expected GuestPort %d, got %d", tt.expected.GuestPort, result.GuestPort)
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
					t.Errorf("expected error, got none")
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
