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
	"strings"
	"testing"

	"github.com/wrr/drop/internal/config"
)

func newTestRuleSet(t *testing.T, defaultAction Action, lines []string) *RuleSet {
	t.Helper()
	rs := newEmptyRuleSet(defaultAction)
	for _, line := range lines {
		r, err := parseRule(line)
		if err != nil {
			t.Fatalf("parseRule(%q): %v", line, err)
		}
		rs.Add(r)
	}
	return rs
}

func TestNewRuleSet(t *testing.T) {
	cases := []struct {
		defaultAction string
		rules         []string
		wantDefault   Action
		want          Action
	}{
		{"allow", nil, Allow, Allow},
		{"deny", nil, Deny, Deny},
		{"ask", nil, Ask, Ask},
		{"deny", []string{"allow example.com"}, Deny, Allow},
		{"allow", []string{"deny example.com"}, Allow, Deny},
		// Rules are added in the config order.
		{"ask", []string{"allow example.com", "deny example.com"}, Ask, Deny},
		{"ask", []string{"deny example.com", "allow example.com"}, Ask, Allow},
	}
	for _, c := range cases {
		cfg := &config.Firewall{DefaultAction: c.defaultAction, Rules: c.rules}
		rs, err := NewRuleSet(cfg)
		if err != nil {
			t.Fatalf("NewRuleSet(%+v): %v", cfg, err)
		}
		if rs.defaultAction != c.wantDefault {
			t.Errorf("NewRuleSet(%+v): default action %v, want %v", cfg, rs.defaultAction, c.wantDefault)
		}
		if got := rs.ResolverAction("example.com"); got != c.want {
			t.Errorf("NewRuleSet(%+v): ResolverAction(example.com) = %v, want %v", cfg, got, c.want)
		}
	}
}

func TestNewRuleSetAddressRule(t *testing.T) {
	rs, err := NewRuleSet(&config.Firewall{DefaultAction: "allow", Rules: []string{"deny 1.2.3.4"}})
	if err != nil {
		t.Fatalf("NewRuleSet: %v", err)
	}
	addr := netip.MustParseAddr("1.2.3.4")
	if got := rs.ConnectionAction("", addr, 443, "tcp"); got != Deny {
		t.Errorf("ConnectionAction(%s) = %v, want %v", addr, got, Deny)
	}
}

func TestNewRuleSetErrors(t *testing.T) {
	cases := []struct {
		cfg     config.Firewall
		wantErr string
	}{
		{config.Firewall{DefaultAction: "maybe"}, `invalid action "maybe"`},
		{config.Firewall{DefaultAction: "ask", Rules: []string{"allow example.com", "allow example.com:0"}},
			`firewall rule "allow example.com:0": invalid port "0"`},
	}
	for _, c := range cases {
		_, err := NewRuleSet(&c.cfg)
		if err == nil {
			t.Errorf("NewRuleSet(%+v): expected an error", c.cfg)
		} else if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("NewRuleSet(%+v): error %q, want %q", c.cfg, err, c.wantErr)
		}
	}
}

func TestResolverAction(t *testing.T) {
	cases := []struct {
		rules []string
		want  Action
	}{
		{nil, Ask},
		{[]string{"allow other.com"}, Ask},
		{[]string{"allow example.com", "deny example.com"}, Deny},
		{[]string{"deny example.com", "allow example.com"}, Allow},
		{[]string{"allow example.com", "deny example.com:80"}, Allow},
		{[]string{"deny example.com", "allow example.com:443"}, Allow},
		{[]string{"deny example.com", "ask example.com:22"}, Ask},
		{[]string{"allow example.com", "ask example.com:22"}, Allow},
		// A later rule replaces an earlier one it fully covers.
		{[]string{"deny example.com", "allow example.com:443", "deny example.com:80,443"}, Deny},
		{[]string{"deny example.com", "allow example.com:80,443", "deny example.com:443"}, Allow},
		{[]string{"deny example.com", "allow example.com:443/udp", "deny example.com:443/tcp"}, Allow},
		{[]string{"deny example.com", "allow example.com/udp", "deny example.com:53/udp"}, Allow},
		{[]string{"deny example.com", "allow example.com:443/tcp", "deny example.com:443"}, Allow},
	}
	for _, c := range cases {
		rs := newTestRuleSet(t, Ask, c.rules)
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
		{nil, "example.com", "1.2.3.4", 443, Ask},
		{[]string{"allow example.com"}, "other.com", "1.2.3.4", 443, Ask},
		{[]string{"allow example.com"}, "example.com", "1.2.3.4", 443, Allow},
		// Address rules win over domain rules.
		{[]string{"deny 1.2.3.4", "allow example.com:443"}, "example.com", "1.2.3.4", 443, Deny},
		{[]string{"ask 1.0.0.0/8", "allow example.com:443"}, "example.com", "1.2.3.4", 443, Ask},
		// A longer prefix wins over a shorter one.
		{[]string{"allow 1.2.3.4", "deny 1.0.0.0/8:443"}, "", "1.2.3.4", 443, Allow},
		{[]string{"allow 1.2.0.0/16", "deny 1.0.0.0/8"}, "", "1.2.3.4", 443, Allow},
		{[]string{"deny 1.0.0.0/8", "allow 1.2.0.0/16"}, "", "1.2.3.4", 443, Allow},
		// Ports win over protocol, which wins over neither.
		{[]string{"allow example.com:443", "deny example.com"}, "example.com", "1.2.3.4", 443, Allow},
		{[]string{"allow example.com:443", "deny example.com"}, "example.com", "1.2.3.4", 80, Deny},
		{[]string{"allow example.com:443", "deny example.com/tcp"}, "example.com", "1.2.3.4", 443, Allow},
		{[]string{"allow 1.2.3.4/tcp", "deny 1.2.3.4"}, "", "1.2.3.4", 443, Allow},
		// Of equally specific rules, the later wins.
		{[]string{"allow 1.2.3.4:443", "deny 1.2.3.4:80,443"}, "", "1.2.3.4", 443, Deny},
		{[]string{"allow example.com:80,443", "deny example.com:443"}, "example.com", "1.2.3.4", 80, Allow},
		{[]string{"allow example.com:80,443", "deny example.com:443"}, "example.com", "1.2.3.4", 443, Deny},
		// IPv4-mapped addresses are unmapped.
		{[]string{"deny 1.2.3.4"}, "", "::ffff:1.2.3.4", 443, Deny},
	}
	for _, c := range cases {
		rs := newTestRuleSet(t, Ask, c.rules)
		addr := netip.MustParseAddr(c.addr)
		if got := rs.ConnectionAction(c.domain, addr, c.port, "tcp"); got != c.want {
			t.Errorf("rules %q: ConnectionAction(%q, %s, %d) = %v, want %v",
				c.rules, c.domain, addr, c.port, got, c.want)
		}
	}
}
