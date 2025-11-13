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
	Source  string
	Target  string
	RW      bool
	Overlay bool
}

type Cwd struct {
	Mounts       []Mount  `toml:"mounts"`
	BlockedPaths []string `toml:"blocked_paths"`
}

type Config struct {
	Mounts         []Mount  `toml:"mounts"`
	BlockedPaths   []string `toml:"blocked_paths"`
	Cwd            Cwd      `toml:"cwd"`
	ExposedEnvVars []string `toml:"exposed_env_vars"`
	Net            Net      `toml:"net"`
}

type PortForward struct {
	HostIP    string
	HostPort  int
	GuestPort int
}

// Custom toml.Unmarshaler for mount field which is of mixed type
// (TOML 1.0 feature). In TOML mount entry can be either a single
// Docker-style mount string source:target:option (like
// "~/go:~/host-go:ro"), or a more verbose TOML table {source =
// "~/go", target = "~/host-go", rw = true, overlay = false}, where
// target is optional and defaults to source if not specified.
func (m *Mount) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		parsed, err := ParseMount(v)
		if err != nil {
			return err
		}
		*m = *parsed
	case map[string]any:
		source, ok := v["source"]
		if !ok {
			return fmt.Errorf("mount config must have 'source' field")
		}

		sourceStr, ok := source.(string)
		if !ok {
			return fmt.Errorf("mount config 'source' must be a string")
		}
		m.Source = sourceStr

		// Target is optional, defaults to source
		if target, exists := v["target"]; exists {
			targetStr, ok := target.(string)
			if !ok {
				return fmt.Errorf("mount config 'target' must be a string")
			}
			m.Target = targetStr
		} else {
			m.Target = sourceStr
		}

		// RW is optional, defaults to false
		if rw, exists := v["rw"]; exists {
			rwBool, ok := rw.(bool)
			if !ok {
				return fmt.Errorf("mount config 'rw' must be a boolean")
			}
			m.RW = rwBool
		}

		// Overlay is optional, defaults to false
		if overlay, exists := v["overlay"]; exists {
			overlayBool, ok := overlay.(bool)
			if !ok {
				return fmt.Errorf("mount config 'overlay' must be a boolean")
			}
			m.Overlay = overlayBool
		}
	default:
		return fmt.Errorf("mount entry should be a string or an object, got %T", data)
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

	if err := validateMounts("mounts", config.Mounts, validateAbsOrHomePath); err != nil {
		return parseError(err)
	}
	if err := validatePaths("blocked_paths", config.BlockedPaths, validateAbsOrHomePath); err != nil {
		return parseError(err)
	}

	if err := validateMounts("cwd.mounts", config.Cwd.Mounts, validateRelPath); err != nil {
		return parseError(err)
	}
	if err := validatePaths("cwd.blocked_paths", config.Cwd.BlockedPaths, validateRelPath); err != nil {
		return parseError(err)
	}

	if err := validateExposedEnvVars(config.ExposedEnvVars); err != nil {
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

// ParseMount parses mount configuration from a string of a form
// source:target:options, where target and options are optional. If
// target is not given or empty, it equals source.  Options are comma
// separated and can be "rw" and "overlay". Valid strings: "~/go",
// "~/go:~/host-go" "~/go:~/host-go:ro", "~/go::ro,overlay
func ParseMount(str string) (*Mount, error) {
	var m Mount
	parts := strings.Split(str, ":")
	parts_cnt := len(parts)
	if parts_cnt > 3 {
		return nil, fmt.Errorf("mount config has too many parts separated by ':', should have at most 3: %v", str)
	}
	m.Source = parts[0]
	if parts_cnt > 1 && parts[1] != "" {
		m.Target = parts[1]
	} else {
		m.Target = m.Source
	}
	if parts_cnt == 3 {
		opts := strings.Split(parts[2], ",")
		for _, opt := range opts {
			switch opt {
			case "rw":
				m.RW = true
			case "overlay":
				m.Overlay = true
			default:
				return nil, fmt.Errorf("not recognized mount option %v in %v. Supported options are 'rw' and 'overlay'", opt, str)
			}
		}
	}
	return &m, nil
}

// validateMounts checks if all mount source and target paths pass the
// provided validation function.
func validateMounts(propId string, mounts []Mount, validateFn func(string) error) error {
	for _, m := range mounts {
		if err := validateFn(m.Source); err != nil {
			return fmt.Errorf("invalid %s '%s': %v", propId, m.Source, err)
		}
		if err := validateFn(m.Target); err != nil {
			return fmt.Errorf("invalid %s '%s': %v", propId, m.Target, err)
		}
	}
	return nil
}

// validatePaths checks if all paths pass the provided
// validation function.
func validatePaths(propId string, paths []string, validateFn func(string) error) error {
	for _, p := range paths {
		if err := validateFn(p); err != nil {
			return fmt.Errorf("invalid %s '%s': %v", propId, p, err)
		}
	}
	return nil
}

func validateAbsOrHomePath(path string) error {
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "~/") {
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

func validateRelPath(path string) error {
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~/") {
		return fmt.Errorf("path must be relative, cannot start with / or ~/")
	}
	if path == "." || path == "./" {
		return nil
	}
	// Allow for trailing / and leading ./
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimPrefix(path, "./")

	if path != filepath.Clean(path) || strings.HasPrefix(path, "..") {
		return fmt.Errorf("path is not normalized")
	}
	return nil
}

// validateExposedEnvVars check if all patterns in the exposed_env_vars list are valid glob patterns.
func validateExposedEnvVars(patterns []string) error {
	for _, pattern := range patterns {
		if _, err := filepath.Match(pattern, "anything"); err != nil {
			return fmt.Errorf("invalid exposed_env_vars pattern '%s': %v", pattern, err)
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
