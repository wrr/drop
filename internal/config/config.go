package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	HomeVisible   []string `toml:"home_visible"`
	HomeWriteable []string `toml:"home_writeable"`
	ProcReadable  []string `toml:"proc_readable"`
	Hide          []string `toml:"hide"`
	EnvExpose     []string `toml:"env_expose"`
}

type PortForward struct {
	HostIP    string
	HostPort  int
	GuestPort int
}

func Read(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %v", err)
	}
	return Parse(string(content))
}

func Parse(configStr string) (*Config, error) {
	var config Config
	if _, err := toml.Decode(configStr, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	if err := validateEnvExpose(config.EnvExpose); err != nil {
		return nil, err
	}

	return &config, nil
}

// parsePortForward parses Docker-like port forwarding syntax
// Supported formats:
// - port (e.g., "8080") -> maps host port 8080 to guest port 8080
// - hostPort:guestPort (e.g., "8080:80") -> maps host port 8080 to guest port 80
// - hostIP:hostPort:guestPort (e.g., "127.0.0.1:8080:80") -> binds to specific host IP
func ParsePortForward(portSpec string) (*PortForward, error) {
	parts := strings.Split(portSpec, ":")
	hostIP := "0.0.0.0"
	if len(parts) >= 2 && strings.Contains(parts[0], ".") {
		hostIP = parts[0]
		parts = parts[1:]
		if net.ParseIP(hostIP) == nil {
			return nil, fmt.Errorf("invalid port forwarding IP address: %s", hostIP)
		}
	}
	if len(parts) == 0 || len(parts) > 2 {
		return nil, fmt.Errorf("invalid port forwarding format: %s", portSpec)
	}
	hostPort, err := toPort(parts[0])
	if err != nil {
		return nil, err
	}
	var guestPort int
	if len(parts) == 2 {
		guestPort, err = toPort(parts[1])
		if err != nil {
			return nil, err
		}
	} else {
		guestPort = hostPort
	}
	return &PortForward{HostIP: hostIP, HostPort: hostPort, GuestPort: guestPort}, nil
}

func toPort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil {
		return -1, fmt.Errorf("invalid port number '%s': %v", s, err)
	}
	if port < 1 || port > 65535 {
		return -1, fmt.Errorf("port number out of range: %d", port)
	}
	return port, nil
}

// validateEnvExpose check if all patterns in the env_expose list are valid glob patterns.
func validateEnvExpose(patterns []string) error {
	for _, pattern := range patterns {
		if _, err := filepath.Match(pattern, "test"); err != nil {
			return fmt.Errorf("invalid env_expose pattern '%s': %v", pattern, err)
		}
	}
	return nil
}
