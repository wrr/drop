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

type Mount struct {
	Source string
	Target string
}

type Config struct {
	MountsRO  []Mount  `toml:"paths_ro"`
	MountsRW  []Mount  `toml:"paths_rw"`
	Blocked   []string `toml:"blocked"`
	EnvExpose []string `toml:"env_expose"`
	Net       Net      `toml:"net"`
}

type PortForward struct {
	HostIP    string
	HostPort  int
	GuestPort int
}

// Custom toml.Unmarshaler for mount field which is of mixed type
// (TOML 1.0 feature). In TOML mount entry can be either a single
// string, like "~/go", which sets mount source and target to be the
// same, or an array ["~/go", "~/host-go"], the first entry is a mount
// source, the second is a target.
func (m *Mount) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		// Single string: source and target are the same
		m.Source = v
		m.Target = v
	case []any:
		// Array of strings: 1 element (same source/target) or 2 elements (different)
		if len(v) == 0 || len(v) > 2 {
			return fmt.Errorf("path array must have 1 or 2 elements, got %d", len(v))
		}

		paths := make([]string, len(v))
		for i, elem := range v {
			str, ok := elem.(string)
			if !ok {
				return fmt.Errorf("path array element %d is not a string", i)
			}
			paths[i] = str
		}

		m.Source = paths[0]
		if len(paths) == 1 {
			m.Target = paths[0]
		} else {
			m.Target = paths[1]
		}
	default:
		return fmt.Errorf("path should be a string or array of strings, got %T", data)
	}
	return nil
}

func Read(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %v", err)
	}
	return Parse(string(content))
}

func parseError(err error) (*Config, error) {
	return nil, fmt.Errorf("failed to parse config: %v", err)
}

func Parse(configStr string) (*Config, error) {
	var config Config
	if _, err := toml.Decode(configStr, &config); err != nil {
		return parseError(err)
	}

	if err := validateMounts("paths_ro", config.MountsRO); err != nil {
		return parseError(err)
	}

	if err := validateMounts("paths_rw", config.MountsRW); err != nil {
		return parseError(err)
	}

	if err := validateEnvExpose(config.EnvExpose); err != nil {
		return parseError(err)
	}

	if config.Net.Mode == "" {
		config.Net.Mode = "isolated"
	}
	if err := ValidateNetworkMode(config.Net.Mode); err != nil {
		return parseError(err)
	}

	if err := ValidatePortForward(config.Net.TCPPortsToHost); err != nil {
		return parseError(fmt.Errorf("invalid tcp_ports_to_host: %v", err))
	}

	if err := ValidatePortForward(config.Net.TCPPortsFromHost); err != nil {
		return parseError(fmt.Errorf("invalid tcp_ports_from_host: %v", err))
	}

	if err := ValidatePortForward(config.Net.UDPPortsToHost); err != nil {
		return parseError(fmt.Errorf("invalid udp_ports_to_host: %v", err))
	}

	if err := ValidatePortForward(config.Net.UDPPortsFromHost); err != nil {
		return parseError(fmt.Errorf("invalid udp_ports_from_host: %v", err))
	}

	return &config, nil
}

// validateMounts checks if all mount source and target paths are normalized and either absolute or start with '~/'.
func validateMounts(propName string, mounts []Mount) error {
	for _, m := range mounts {
		if err := validatePathEntry(m.Source); err != nil {
			return fmt.Errorf("invalid %s '%s': %v", propName, m.Source, err)
		}
		if err := validatePathEntry(m.Target); err != nil {
			return fmt.Errorf("invalid %s '%s': %v", propName, m.Target, err)
		}
	}
	return nil
}

// validatePaths checks if all paths are normalized and either absolute or start with '~/'.
func validatePaths(propName string, paths []string) error {
	for _, p := range paths {
		if err := validatePathEntry(p); err != nil {
			return fmt.Errorf("invalid %s '%s': %v", propName, p, err)
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
