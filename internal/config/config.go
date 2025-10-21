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

type Net struct {
	Mode             string   `toml:"mode"`
	TCPPortsToHost   []string `toml:"tcp_ports_to_host"`
	TCPPortsFromHost []string `toml:"tcp_ports_from_host"`
	UDPPortsToHost   []string `toml:"udp_ports_to_host"`
	UDPPortsFromHost []string `toml:"udp_ports_from_host"`
}

type Config struct {
	PathsRO   []string `toml:"paths_ro"`
	PathsRW   []string `toml:"paths_rw"`
	Blocked   []string `toml:"blocked"`
	EnvExpose []string `toml:"env_expose"`
	Net       Net      `toml:"net"`
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

	if err := validatePaths("paths_ro", config.PathsRO); err != nil {
		return nil, err
	}

	if err := validatePaths("paths_rw", config.PathsRW); err != nil {
		return nil, err
	}

	if err := validateEnvExpose(config.EnvExpose); err != nil {
		return nil, err
	}

	if config.Net.Mode == "" {
		config.Net.Mode = "isolated"
	}
	if err := ValidateNetworkMode(config.Net.Mode); err != nil {
		return nil, err
	}

	if err := ValidatePortForward(config.Net.TCPPortsToHost); err != nil {
		return nil, fmt.Errorf("invalid tcp_ports_to_host: %v", err)
	}

	if err := ValidatePortForward(config.Net.TCPPortsFromHost); err != nil {
		return nil, fmt.Errorf("invalid tcp_ports_from_host: %v", err)
	}

	if err := ValidatePortForward(config.Net.UDPPortsToHost); err != nil {
		return nil, fmt.Errorf("invalid udp_ports_to_host: %v", err)
	}

	if err := ValidatePortForward(config.Net.UDPPortsFromHost); err != nil {
		return nil, fmt.Errorf("invalid udp_ports_from_host: %v", err)
	}

	return &config, nil
}

// validatePaths checks if all paths are normalized and either absolute or start with '~/'.
func validatePaths(prop_name string, paths []string) error {
	for _, p := range paths {
		if err := validatePathEntry(p); err != nil {
			return fmt.Errorf("invalid %s '%s': %v", prop_name, p, err)
		}
	}
	return nil
}

func validatePathEntry(path string) error {
	if !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must start with / or ~/")
	}
	if path == "/" {
		return fmt.Errorf("cannot expose the whole root directory")
	}
	if path == "~/" {
		return fmt.Errorf("cannot expose the whole home directory")
	}

	// Remove ~ for validation with Clean()
	path = strings.TrimPrefix(path, "~")

	// filepath.Clean() removes trailing / from all paths except /.  We
	// allow for trailing /, so we remove it before validation.
	path = strings.TrimSuffix(path, "/")

	if path != filepath.Clean(path) {
		return fmt.Errorf("path is not normalized")
	}
	return nil
}

// validateEnvExpose check if all patterns in the env_expose list are valid glob patterns.
func validateEnvExpose(patterns []string) error {
	for _, pattern := range patterns {
		if _, err := filepath.Match(pattern, "anything"); err != nil {
			return fmt.Errorf("invalid env_expose pattern '%s': %v", pattern, err)
		}
	}
	return nil
}

// ValidateNetworkMode validates that the network mode is one of the allowed values.
func ValidateNetworkMode(mode string) error {
	switch mode {
	case "off", "isolated", "unjailed":
		return nil
	default:
		return fmt.Errorf("invalid network mode '%s': must be 'off', 'isolated', or 'unjailed'", mode)
	}
}

// ValidatePortForward validates Pasta-like port forwarding syntax.
//
// To keep things simple and keep an option of using a different
// connectivity tool, only the simplest Pasta mapping expressions are
// allowed.
//
// Supported format of forwardSpecs items:
//   - port (e.g., "8080") -> maps host port 8080 to guest port 8080
//   - hostPort:guestPort (e.g., "8080:80") -> maps host port 8080 to guest port 80
//   - hostIP/hostPort:guestPort (e.g., "127.0.0.1/8080:80") -> maps
//     host port 8080 bound to IP address 127.0.0.1 to gues port 80
//   - "none" -> disables port mapping
//   - "auto" -> automatically forwards all open ports
func ValidatePortForward(forwardSpecs []string) error {
	hasAuto := false
	hasNone := false

	for _, mapping := range forwardSpecs {
		mapping = strings.TrimSpace(mapping)
		if mapping == "auto" {
			hasAuto = true
			continue
		}
		if mapping == "none" {
			hasNone = true
			continue
		}
		portPart := mapping

		// Check if host IP is specified
		if strings.Contains(mapping, "/") {
			parts := strings.Split(mapping, "/")
			if len(parts) != 2 {
				return fmt.Errorf("invalid port forwarding format: %s", mapping)
			}
			hostIP := parts[0]
			portPart = parts[1]
			if net.ParseIP(hostIP) == nil {
				return fmt.Errorf("invalid port forwarding IP address: %s", hostIP)
			}
		}

		parts := strings.Split(portPart, ":")
		if len(parts) == 0 || len(parts) > 2 {
			return fmt.Errorf("invalid port forwarding format: %s", mapping)
		}

		for i := range len(parts) {
			if err := validatePort(parts[i]); err != nil {
				return err
			}
		}
	}

	if hasAuto && len(forwardSpecs) > 1 {
		return fmt.Errorf("\"auto\" must be the only port forwarding rule")
	}
	if hasNone && len(forwardSpecs) > 1 {
		return fmt.Errorf("\"none\" must be the only port forwarding rule")
	}

	return nil
}

func validatePort(s string) error {
	port, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("invalid port number '%s': %v", s, err)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port number out of range: %d", port)
	}
	return nil
}
