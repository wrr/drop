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
	"net/netip"
	"testing"
)

func newTestRuleSet(t *testing.T, defaultAction Action, lines []string) *RuleSet {
	t.Helper()
	rs := NewRuleSet(defaultAction)
	for _, line := range lines {
		r, err := parseRule(line)
		if err != nil {
			t.Fatalf("parseRule(%q): %v", line, err)
		}
		rs.Add(r)
	}
	return rs
}

func TestResolverAction(t *testing.T) {
	cases := []struct {
		rules []string
		want  Action
	}{
		{nil, ask},
		{[]string{"allow other.com"}, ask},
		{[]string{"allow example.com", "deny example.com"}, deny},
		{[]string{"deny example.com", "allow example.com"}, allow},
		{[]string{"allow example.com", "deny example.com:80"}, allow},
		{[]string{"deny example.com", "allow example.com:443"}, allow},
		{[]string{"deny example.com", "ask example.com:22"}, ask},
		{[]string{"allow example.com", "ask example.com:22"}, allow},
		// A later rule replaces an earlier one it fully covers.
		{[]string{"deny example.com", "allow example.com:443", "deny example.com:80,443"}, deny},
		{[]string{"deny example.com", "allow example.com:80,443", "deny example.com:443"}, allow},
		{[]string{"deny example.com", "allow example.com:443/udp", "deny example.com:443/tcp"}, allow},
		{[]string{"deny example.com", "allow example.com/udp", "deny example.com:53/udp"}, allow},
		{[]string{"deny example.com", "allow example.com:443/tcp", "deny example.com:443"}, allow},
	}
	for _, c := range cases {
		rs := newTestRuleSet(t, ask, c.rules)
		if got := rs.ResolverAction("example.com"); got != c.want {
			t.Errorf("rules %q: ResolverAction(example.com) = %v, want %v", c.rules, got, c.want)
		}
	}
}

func TestConnectionAction(t *testing.T) {
	cases := []struct {
		rules  []string
		domain string
		addr   string
		port   int
		want   Action
	}{
		{nil, "example.com", "1.2.3.4", 443, ask},
		{[]string{"allow example.com"}, "other.com", "1.2.3.4", 443, ask},
		{[]string{"allow example.com"}, "example.com", "1.2.3.4", 443, allow},
		// Address rules win over domain rules.
		{[]string{"deny 1.2.3.4", "allow example.com:443"}, "example.com", "1.2.3.4", 443, deny},
		{[]string{"ask 1.0.0.0/8", "allow example.com:443"}, "example.com", "1.2.3.4", 443, ask},
		// A longer prefix wins over a shorter one.
		{[]string{"allow 1.2.3.4", "deny 1.0.0.0/8:443"}, "", "1.2.3.4", 443, allow},
		{[]string{"allow 1.2.0.0/16", "deny 1.0.0.0/8"}, "", "1.2.3.4", 443, allow},
		{[]string{"deny 1.0.0.0/8", "allow 1.2.0.0/16"}, "", "1.2.3.4", 443, allow},
		// Ports win over protocol, which wins over neither.
		{[]string{"allow example.com:443", "deny example.com"}, "example.com", "1.2.3.4", 443, allow},
		{[]string{"allow example.com:443", "deny example.com"}, "example.com", "1.2.3.4", 80, deny},
		{[]string{"allow example.com:443", "deny example.com/tcp"}, "example.com", "1.2.3.4", 443, allow},
		{[]string{"allow 1.2.3.4/tcp", "deny 1.2.3.4"}, "", "1.2.3.4", 443, allow},
		// Of equally specific rules, the later wins.
		{[]string{"allow 1.2.3.4:443", "deny 1.2.3.4:80,443"}, "", "1.2.3.4", 443, deny},
		{[]string{"allow example.com:80,443", "deny example.com:443"}, "example.com", "1.2.3.4", 80, allow},
		{[]string{"allow example.com:80,443", "deny example.com:443"}, "example.com", "1.2.3.4", 443, deny},
		// IPv4-mapped addresses are unmapped.
		{[]string{"deny 1.2.3.4"}, "", "::ffff:1.2.3.4", 443, deny},
	}
	for _, c := range cases {
		rs := newTestRuleSet(t, ask, c.rules)
		addr := netip.MustParseAddr(c.addr)
		if got := rs.ConnectionAction(c.domain, addr, c.port, "tcp"); got != c.want {
			t.Errorf("rules %q: ConnectionAction(%q, %s, %d) = %v, want %v",
				c.rules, c.domain, addr, c.port, got, c.want)
		}
	}
}
