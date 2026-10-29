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

package firewall

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

type Action int

// The order matters: if the rules are equally specific, the one
// with the lowest action is used.
const (
	deny Action = iota
	allow
	ask
)

func (a Action) String() string {
	switch a {
	case allow:
		return "allow"
	case deny:
		return "deny"
	case ask:
		return "ask"
	default:
		return fmt.Sprintf("action(%d)", int(a))
	}
}

func parseAction(s string) (Action, error) {
	switch s {
	case "allow":
		return allow, nil
	case "deny":
		return deny, nil
	case "ask":
		return ask, nil
	}
	return deny, fmt.Errorf("invalid action %q, expected allow, deny or ask", s)
}

// Rule defines an action to take by the firewall and the traffic
// destination to which the action applies.
type Rule struct {
	action Action
	// A lowercase domain name without trailing dot, empty if addr is set.
	domain string
	// An IP address or address range, invalid if domain is set.
	addr netip.Prefix
	// Empty means all the ports.
	ports []int
	// "tcp" or "udp", empty means both
	proto string
	// Where the rule comes from.
	origin string
}

// parseRule parses a firewall rule from a string config line.
// Example valid strings:
//
//	deny   example.com
//	allow  registry.npmjs.org:80,443
//	allow  example.com:8443/udp
//	allow  home.example.com:8080
//	allow  73.23.75.23:45
//	ask    192.168.0.0/16
//	allow  192.168.0.0/16:80/tcp
func parseRule(line string) (Rule, error) {
	var r Rule
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return Rule{}, fmt.Errorf("expected action and target")
	}
	if len(fields) > 2 {
		return Rule{}, fmt.Errorf("unexpected %q after the target", fields[2])
	}
	action, err := parseAction(fields[0])
	if err != nil {
		return Rule{}, err
	}
	r.action = action

	target := strings.ToLower(fields[1])
	// The protocol is after the last slash, unless that slash is the
	// one of an IP address range (/16).
	if i := strings.LastIndex(target, "/"); i != -1 && isLetters(target[i+1:]) {
		proto := target[i+1:]
		if proto != "tcp" && proto != "udp" {
			return Rule{}, fmt.Errorf("invalid protocol %q, expected tcp or udp", proto)
		}
		target = target[:i]
		r.proto = proto
	}
	// The ports part is after the last colon which is not within
	// bracketed IPv6 addresses.
	if i := strings.LastIndex(target, ":"); i != -1 && !strings.HasSuffix(target, "]") {
		for _, p := range strings.Split(target[i+1:], ",") {
			port, err := stringToPort(p)
			if err != nil {
				return Rule{}, err
			}
			r.ports = append(r.ports, port)
		}
		target = target[:i]
	}
	target, bracketed, err := removeBrackets(target)
	if err != nil {
		return Rule{}, err
	}

	if addr, err := netip.ParseAddr(target); err == nil {
		if addr.Zone() != "" {
			return Rule{}, fmt.Errorf("IPv6 zone in %q is not supported", target)
		}
		if addr.Is4In6() {
			return Rule{}, fmt.Errorf("IPv4-mapped address %q is not supported, use %s",
				target, addr.Unmap())
		}
		r.addr = netip.PrefixFrom(addr, addr.BitLen())
	} else if strings.Contains(target, "/") {
		prefix, err := netip.ParsePrefix(target)
		if err != nil {
			return Rule{}, fmt.Errorf("invalid address range %q", target)
		}
		if prefix.Addr().Is4In6() {
			return Rule{}, fmt.Errorf("IPv4-mapped address range %q is not supported", target)
		}
		if prefix != prefix.Masked() {
			return Rule{}, fmt.Errorf("address range %q has host bits set, expected %s",
				target, prefix.Masked())
		}
		r.addr = prefix
	} else {
		r.domain = strings.TrimSuffix(target, ".")
		if !isValidDomain(r.domain) {
			return Rule{}, fmt.Errorf("invalid domain %q", r.domain)
		}
	}
	if bracketed && !r.addr.Addr().Is6() {
		return Rule{}, fmt.Errorf("brackets are only for IPv6 addresses: %q", target)
	}
	return r, nil
}

func removeBrackets(s string) (string, bool, error) {
	hasOpening := strings.HasPrefix(s, "[")
	hasClosing := strings.HasSuffix(s, "]")
	if !hasOpening {
		if hasClosing {
			return "", false, fmt.Errorf("missing [ in %q", s)
		}
		return s, false, nil
	}
	if !hasClosing {
		return "", false, fmt.Errorf("missing ] in %q", s)
	}
	return s[1 : len(s)-1], true, nil
}

func stringToPort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return port, nil
}

func isLetters(s string) bool {
	return s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyz") == ""
}
