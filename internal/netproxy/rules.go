// Copyright 2026 Jan Wrobel <jan@mixedbit.org>
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

package netproxy

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// DefaultPorts are the ports allowed for a rule that doesn't specify
// a port explicitly.
var DefaultPorts = []int{80, 443}

// Rule is a single allowed_domains entry.
type Rule struct {
	// Host is a lower-case domain name (without the "*." prefix for
	// wildcard rules) or an IP address.
	Host string
	// Wildcard rules match all subdomains of Host, but not Host itself.
	Wildcard bool
	// Port is the only port allowed by the rule. 0 means DefaultPorts.
	Port int
}

// ParseRule parses an allowed_domains entry. Supported forms:
//
//   - "example.com" - example.com on ports 80 and 443
//   - "*.example.com" - all subdomains of example.com on ports 80 and 443
//   - "example.com:8080" - example.com on port 8080 only
//   - "192.0.2.1", "[2001:db8::1]:8443" - IP addresses
func ParseRule(entry string) (Rule, error) {
	var r Rule
	s := strings.ToLower(strings.TrimSpace(entry))
	if s == "" {
		return r, fmt.Errorf("empty entry")
	}
	// Reject URLs (like "https://example.com/path") before splitting
	// the port, which would misinterpret the scheme as a host.
	if strings.Contains(s, "/") {
		return r, fmt.Errorf("invalid domain '%s'", entry)
	}

	host := s
	if h, p, err := net.SplitHostPort(s); err == nil {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return r, fmt.Errorf("invalid port in '%s'", entry)
		}
		host = h
		r.Port = port
	} else if strings.HasPrefix(s, "[") {
		host = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		r.Host = addr.Unmap().String()
		return r, nil
	}

	if rest, ok := strings.CutPrefix(host, "*."); ok {
		r.Wildcard = true
		host = rest
	}
	host = strings.TrimSuffix(host, ".")
	if !isValidDomain(host) {
		return r, fmt.Errorf("invalid domain '%s'", entry)
	}
	r.Host = host
	return r, nil
}

func isValidDomain(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, c := range label {
			isAlnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
			if !isAlnum && c != '-' && c != '_' {
				return false
			}
		}
	}
	return true
}

// Allowlist is a list of rules, a destination is allowed if any of
// the rules matches it.
type Allowlist []Rule

// ParseAllowlist parses allowed_domains entries.
func ParseAllowlist(entries []string) (Allowlist, error) {
	var list Allowlist
	for _, e := range entries {
		r, err := ParseRule(e)
		if err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, nil
}

// Allows returns true if connections to host:port are allowed.
func (a Allowlist) Allows(host string, port int) bool {
	host = normalizeHost(host)
	for _, r := range a {
		if r.matchesHost(host) && r.matchesPort(port) {
			return true
		}
	}
	return false
}

// Ports returns sorted ports allowed by the rules.
func (a Allowlist) Ports() []int {
	var ports []int
	for _, r := range a {
		if r.Port != 0 {
			ports = append(ports, r.Port)
		} else {
			ports = append(ports, DefaultPorts...)
		}
	}
	slices.Sort(ports)
	return slices.Compact(ports)
}

// allowsName returns true if name is matched by a domain rule, on
// any port.
func (a Allowlist) allowsName(name string) bool {
	name = normalizeHost(name)
	for _, r := range a {
		if r.matchesHost(name) {
			return true
		}
	}
	return false
}

// allowsExactDomain returns true if host:port is matched by a rule
// that is not a wildcard.
func (a Allowlist) allowsExactDomain(host string, port int) bool {
	host = normalizeHost(host)
	for _, r := range a {
		if !r.Wildcard && r.Host == host && r.matchesPort(port) {
			return true
		}
	}
	return false
}

func (r Rule) matchesHost(host string) bool {
	if r.Wildcard {
		return strings.HasSuffix(host, "."+r.Host)
	}
	return host == r.Host
}

func (r Rule) matchesPort(port int) bool {
	if r.Port != 0 {
		return port == r.Port
	}
	for _, p := range DefaultPorts {
		if port == p {
			return true
		}
	}
	return false
}

// normalizeHost lower-cases a host, removes the trailing dot and IPv6
// brackets and converts IPv4-mapped IPv6 addresses to IPv4.
func normalizeHost(host string) string {
	host = strings.ToLower(host)
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap().String()
	}
	return strings.TrimSuffix(host, ".")
}

// isIPLiteral returns true if host is an IP address, not a domain name.
func isIPLiteral(host string) bool {
	_, err := netip.ParseAddr(normalizeHost(host))
	return err == nil
}
