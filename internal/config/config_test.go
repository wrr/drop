package config

import (
	"strings"
	"testing"
)

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
