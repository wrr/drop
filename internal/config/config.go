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
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/wrr/drop/internal/osutil"
)

type Config struct {
	Extends      string   `toml:"extends"`
	Mounts       []Mount  `toml:"mounts"`
	BlockedPaths []string `toml:"blocked_paths"`
	Cwd          Cwd      `toml:"cwd"`
	Environ      Environ  `toml:"environ"`
	Net          Net      `toml:"net"`
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

type Environ struct {
	ExposedVars []string `toml:"exposed_vars"`
	SetVars     []EnvVar `toml:"set_vars"`
}

type EnvVar struct {
	Name  string
	Value string
}

type Net struct {
	Mode              string          `toml:"mode"`
	TCPPublishedPorts []PublishedPort `toml:"tcp_published_ports"`
	TCPHostPorts      []HostPort      `toml:"tcp_host_ports"`
	UDPPublishedPorts []PublishedPort `toml:"udp_published_ports"`
	UDPHostPorts      []HostPort      `toml:"udp_host_ports"`
}

type PublishedPort struct {
	Auto      bool
	HostIP    string
	HostPort  int
	GuestPort int
}

// Different from PublishedPort because Pasta, contrary to its man
// page, doesn't seem to support HostIP for ports forwarded from host
// to the namespace.
type HostPort struct {
	Auto      bool
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
		parsed, err := ParseMountCompact(v)
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

func (e *EnvVar) Expand(mapping func(string) string) string {
	return fmt.Sprintf("%s=%s", e.Name, os.Expand(e.Value, mapping))
}

func (e *EnvVar) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		name, value, found := strings.Cut(v, "=")
		if !found {
			return fmt.Errorf("environment variable should have a name=value form, got %s", v)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("environment variable name should not be empty, got %s", v)
		}
		e.Name = name
		e.Value = value
	default:
		return fmt.Errorf("environment variable should be a string, got %T", data)
	}
	return nil
}

// Custom toml.Unmarshaler for PublishedPort field. Input entry is a
// string in the format [hostIP/][hostPort:]guestPort or "auto" for
// automatic port forwarding of all ports.
func (p *PublishedPort) UnmarshalTOML(data any) error {
	str, ok := data.(string)
	if !ok {
		return fmt.Errorf("published port entry should be a string, got %T", data)
	}
	parsed, err := ParsePublishedPort(str)
	if err != nil {
		return err
	}
	*p = *parsed
	return nil
}

// Custom toml.Unmarshaler for HostPort field. Input is a string in
// the format hostPort[:guestPort] or "auto" for automatic port
// forwarding of all ports.
func (p *HostPort) UnmarshalTOML(data any) error {
	str, ok := data.(string)
	if !ok {
		return fmt.Errorf("host port entry should be a string, got %T", data)
	}
	parsed, err := ParseHostPort(str)
	if err != nil {
		return err
	}
	*p = *parsed
	return nil
}

type reader struct {
	files    map[string]bool
	homeDir  string
	readFile func(name string) ([]byte, error)
}

func Read(path string, homeDir string) (*Config, error) {
	r := &reader{
		files:    make(map[string]bool),
		homeDir:  homeDir,
		readFile: os.ReadFile,
	}
	config, err := r.read(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %v", path, err)
	}
	return config, err
}

func (r *reader) read(path string) (*Config, error) {
	if r.files[path] {
		return nil, fmt.Errorf("circular 'extends': %s already included", path)
	}
	content, err := r.readFile(path)
	if err != nil {
		return nil, err
	}
	r.files[path] = true
	configDir := filepath.Dir(path)
	return r.parse(string(content), configDir)
}

func (r *reader) readBase(path string, configDir string) (*Config, error) {
	if osutil.IsRootOrHomeSubPath(path) {
		if err := osutil.ValidateRootOrHomeSubPath(path); err != nil {
			return nil, fmt.Errorf("extends path %s invalid: %v", path, err)
		}
		path = osutil.TildeToHomeDir(path, r.homeDir)
	} else {
		if err := osutil.ValidateRelPath(path); err != nil {
			return nil, fmt.Errorf("extends path %s invalid: %v", path, err)
		}
		path = filepath.Join(configDir, path)
	}
	return r.read(path)
}

func (r *reader) parse(configStr string, configDir string) (*Config, error) {
	var cfg *Config = &Config{}
	meta, err := toml.Decode(configStr, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("unrecognized key: %s", undecoded[0].String())
	}
	if cfg.Extends != "" {
		base, err := r.readBase(cfg.Extends, configDir)
		if err != nil {
			return nil, err
		}
		cfg = merge(base, cfg)
	}

	if cfg.Net.Mode == "" {
		cfg.Net.Mode = "isolated"
	}

	if err := Validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func merge(base *Config, sub *Config) *Config {
	netMode := base.Net.Mode
	if sub.Net.Mode != "" {
		netMode = sub.Net.Mode
	}
	return &Config{
		Extends:      sub.Extends,
		Mounts:       slices.Concat(base.Mounts, sub.Mounts),
		BlockedPaths: slices.Concat(base.BlockedPaths, sub.BlockedPaths),
		Cwd: Cwd{
			Mounts:       slices.Concat(base.Cwd.Mounts, sub.Cwd.Mounts),
			BlockedPaths: slices.Concat(base.Cwd.BlockedPaths, sub.Cwd.BlockedPaths),
		},
		Environ: Environ{
			ExposedVars: slices.Concat(base.Environ.ExposedVars, sub.Environ.ExposedVars),
			SetVars:     slices.Concat(base.Environ.SetVars, sub.Environ.SetVars),
		},
		Net: Net{
			Mode:              netMode,
			TCPPublishedPorts: slices.Concat(base.Net.TCPPublishedPorts, sub.Net.TCPPublishedPorts),
			TCPHostPorts:      slices.Concat(base.Net.TCPHostPorts, sub.Net.TCPHostPorts),
			UDPPublishedPorts: slices.Concat(base.Net.UDPPublishedPorts, sub.Net.UDPPublishedPorts),
			UDPHostPorts:      slices.Concat(base.Net.UDPHostPorts, sub.Net.UDPHostPorts),
		},
	}
}

func Validate(cfg *Config) error {
	if err := validateMounts("mounts", cfg.Mounts, osutil.ValidateRootOrHomeSubPath); err != nil {
		return err
	}
	if err := validatePaths("blocked_paths", cfg.BlockedPaths, osutil.ValidateRootOrHomeSubPath); err != nil {
		return err
	}

	if err := validateMounts("cwd.mounts", cfg.Cwd.Mounts, osutil.ValidateRelPath); err != nil {
		return err
	}
	if err := validatePaths("cwd.blocked_paths", cfg.Cwd.BlockedPaths, osutil.ValidateRelPath); err != nil {
		return err
	}

	if err := validateEnvironExposedVars(cfg.Environ.ExposedVars); err != nil {
		return err
	}
	if err := validateNetworkMode(cfg.Net.Mode); err != nil {
		return err
	}

	if err := validatePublishedPorts(cfg.Net.TCPPublishedPorts); err != nil {
		return fmt.Errorf("invalid tcp_published_ports: %v", err)
	}

	if err := validateHostPorts(cfg.Net.TCPHostPorts); err != nil {
		return fmt.Errorf("invalid tcp_host_ports: %v", err)
	}

	if err := validatePublishedPorts(cfg.Net.UDPPublishedPorts); err != nil {
		return fmt.Errorf("invalid udp_published_ports: %v", err)
	}

	if err := validateHostPorts(cfg.Net.UDPHostPorts); err != nil {
		return fmt.Errorf("invalid udp_host_ports: %v", err)
	}
	return nil
}

// ParseMountCompact parses mount configuration from a string of a form
// source:target:options, where target and options are optional. If
// target is not given or empty, it equals source.  Options are comma
// separated and can be "rw" and "overlay". Valid strings: "~/go",
// "~/go:~/host-go" "~/go:~/host-go:ro", "~/go::ro,overlay
func ParseMountCompact(str string) (*Mount, error) {
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

// ParsePublishedPort parses and validates a published port string.
//
// To keep things simple and keep an option of using a different
// connectivity tool, only the simplest Pasta mapping expressions are
// allowed.
//
// Supported format of forwardSpecs items:
//   - port (e.g., "8080") -> publishes Drop port 8080 as host port
//     8080 bound to 127.0.0.1 only
//   - hostPort:guestPort (e.g., "8080:80") -> publishes Drop port 80
//     as host port 8080 bound to 127.0.0.1 only
//   - hostIP/hostPort:guestPort (e.g., "192.168.0.3/8080:80") ->
//     published Drop port 80 as host port 8080 bound to IP address
//     192.168.0.3
//   - "auto" -> automatically publishes all open ports and binds them
//     to ALL available IP addresses
func ParsePublishedPort(spec string) (*PublishedPort, error) {
	var p PublishedPort

	spec = strings.TrimSpace(spec)
	if spec == "auto" {
		p.Auto = true
		return &p, nil
	}
	p.HostIP = "127.0.0.1"
	portPart := spec

	// Check if host IP is specified
	if strings.Contains(spec, "/") {
		parts := strings.Split(spec, "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid port publish format: %s", spec)
		}
		p.HostIP = parts[0]
		portPart = parts[1]
		if net.ParseIP(p.HostIP) == nil {
			return nil, fmt.Errorf("invalid port publish IP address: %s", p.HostIP)
		}
	}

	hostPort, guestPort, err := parsePortPair(portPart)
	if err != nil {
		return nil, err
	}
	p.HostPort = hostPort
	p.GuestPort = guestPort
	return &p, nil
}

// ParseHostPort parses host port string. Like ParsePublishedPort,
// but does not allow to specify hostIP/, only HOST_PORT[:DROP_PORT] or "auto".
func ParseHostPort(spec string) (*HostPort, error) {
	var p HostPort

	spec = strings.TrimSpace(spec)
	if spec == "auto" {
		p.Auto = true
		return &p, nil
	}

	hostPort, guestPort, err := parsePortPair(spec)
	if err != nil {
		return nil, err
	}
	p.HostPort = hostPort
	p.GuestPort = guestPort
	return &p, nil
}

// parsePortPair is a helper for parsing HOST_PORT[:DROP_PORT] string
// into two port ints
func parsePortPair(s string) (int, int, error) {
	parts := strings.Split(s, ":")
	if len(parts) == 0 || len(parts) > 2 {
		return -1, -1, fmt.Errorf("invalid port forwarding format: %s", s)
	}
	hostPort, err := parsePort(parts[0])
	if err != nil {
		return -1, -1, err
	}
	guestPort := hostPort
	if len(parts) == 2 {
		guestPort, err = parsePort(parts[1])
		if err != nil {
			return -1, -1, err
		}
	}
	return hostPort, guestPort, nil

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

// validateEnvironExposedVars check if all patterns in the
// exposed_vars list are valid glob patterns.
func validateEnvironExposedVars(patterns []string) error {
	for _, pattern := range patterns {
		if _, err := filepath.Match(pattern, "anything"); err != nil {
			return fmt.Errorf("invalid exposed_env_vars pattern '%s': %v", pattern, err)
		}
	}
	return nil
}

// validateNetworkMode validates that the network mode is one of the allowed values.
func validateNetworkMode(mode string) error {
	switch mode {
	case "off", "isolated", "unjailed":
		return nil
	default:
		return fmt.Errorf("invalid network mode '%s': must be 'off' or 'isolated'", mode)
	}
}

// validatePublishedPorts validates published port list. The list
// individual entries are already validated during PublishedPort items
// parsing, this function checks only list-level constraints
// (ensures "auto" is the only rule if present).
func validatePublishedPorts(mappings []PublishedPort) error {
	for _, m := range mappings {
		if m.Auto && len(mappings) > 1 {
			return fmt.Errorf("\"auto\" must be the only published port entry")
		}
	}
	return nil
}

// validateHostPorts validates host port forwarding list. The list
// individual entries are already validated during HostPort items
// parsing, this function checks only list-level constraints (
// ensures "auto" is the only rule if present).
func validateHostPorts(mappings []HostPort) error {
	for _, m := range mappings {
		if m.Auto && len(mappings) > 1 {
			return fmt.Errorf("\"auto\" must be the only host port entry")
		}
	}
	return nil
}

func parsePort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil {
		return -1, fmt.Errorf("invalid port number '%s': %v", s, err)
	}
	if port < 1 || port > 65535 {
		return -1, fmt.Errorf("port number out of range: %d", port)
	}
	return port, nil
}
