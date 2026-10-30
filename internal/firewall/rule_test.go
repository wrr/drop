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
	"reflect"
	"strings"
	"testing"
)

func TestParseAction(t *testing.T) {
	cases := []struct {
		in, want, wantErr string
	}{
		{"allow", "allow", ""},
		{"deny", "deny", ""},
		{"ask", "ask", ""},
		{"block", "", "invalid action \"block\""},
	}
	for _, c := range cases {
		got, err := parseAction(c.in)
		switch {
		case c.wantErr != "" && err == nil:
			t.Errorf("parseAction(%q): expected an error", c.in)
		case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
			t.Errorf("parseAction(%q): error %q, want %q", c.in, err, c.wantErr)
		case c.wantErr == "" && err != nil:
			t.Errorf("parseAction(%q): unexpected error %v", c.in, err)
		case c.wantErr == "" && got.String() != c.want:
			t.Errorf("parseAction(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestParseRule(t *testing.T) {
	prefix := netip.MustParsePrefix
	cases := []struct {
		line string
		want Rule
	}{
		{"deny telemetry.example.com",
			Rule{action: Deny, domain: "telemetry.example.com"}},
		{"allow registry.npmjs.org:443,21,156",
			Rule{action: Allow, domain: "registry.npmjs.org", ports: []int{21, 156, 443}}},
		{"allow example.com:65535",
			Rule{action: Allow, domain: "example.com", ports: []int{65535}}},
		{"allow example.com:8443/udp",
			Rule{action: Allow, domain: "example.com", ports: []int{8443}, proto: "udp"}},
		{"ask google.com/tcp",
			Rule{action: Ask, domain: "google.com", proto: "tcp"}},
		{"allow EXAMPLE.com.",
			Rule{action: Allow, domain: "example.com"}},
		{"allow 73.23.75.23:45",
			Rule{action: Allow, addr: prefix("73.23.75.23/32"), ports: []int{45}}},
		{"allow [fd00::1]",
			Rule{action: Allow, addr: prefix("fd00::1/128")}},
		{"ask 192.168.0.0/16",
			Rule{action: Ask, addr: prefix("192.168.0.0/16")}},
		{"allow 10.0.0.0/8:443/udp",
			Rule{action: Allow, addr: prefix("10.0.0.0/8"), ports: []int{443}, proto: "udp"}},
		{"allow [fd00::/8]:443",
			Rule{action: Allow, addr: prefix("fd00::/8"), ports: []int{443}}},
	}
	for _, c := range cases {
		got, err := parseRule(c.line)
		if err != nil {
			t.Errorf("parseRule(%q): unexpected error %v", c.line, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseRule(%q) = %+v, want %+v", c.line, got, c.want)
		}
	}
}

func TestParseRuleErrors(t *testing.T) {
	cases := []struct {
		line, wantErr string
	}{
		{"allow", "expected action and target"},
		{"maybe example.com", `invalid action "maybe"`},
		{"allow example.com:0", `invalid port "0"`},
		{"allow example.com:65536", `invalid port "65536"`},
		{"allow example.com:http", `invalid port "http"`},
		{"allow example.com/sctp", `invalid protocol "sctp"`},
		{"allow example.com:443 forever", `unexpected "forever"`},
		{"ask 192.168.1.1/16", "has host bits set, expected 192.168.0.0/16"},
		{"ask 192.168.0.0/33", `invalid address range "192.168.0.0/33"`},
		{"allow [fe80::1%eth0]", "IPv6 zone"},
		{"allow [::ffff:192.0.2.1]", "is not supported, use 192.0.2.1"},
		{"allow [::ffff:10.0.0.0/104]", "IPv4-mapped address range"},
		{"allow *.example.com", `invalid domain "*.example.com"`},
		{"allow [example.com]", "brackets are only for IPv6"},
		{"allow [192.0.2.1]", "brackets are only for IPv6"},
		{"allow [10.0.0.0/8]", "brackets are only for IPv6"},
		{"allow fd00::1]", "missing ["},
		{"allow [fd00::1:443", "missing ]"},
	}
	for _, c := range cases {
		_, err := parseRule(c.line)
		if err == nil {
			t.Errorf("parseRule(%q): expected an error", c.line)
		} else if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("parseRule(%q): error %q, want %q", c.line, err, c.wantErr)
		}
	}
}

func TestRuleMatches(t *testing.T) {
	cases := []struct {
		rule   string
		domain string
		addr   string
		port   int
		proto  string
		want   bool
	}{
		{"allow example.com", "example.com", "1.2.3.4", 443, "tcp", true},
		{"allow example.com", "other.com", "1.2.3.4", 443, "tcp", false},
		{"allow example.com", "a.example.com", "1.2.3.4", 443, "tcp", false},
		{"allow example.com", "", "1.2.3.4", 443, "tcp", false},
		// A protocol other than tcp and udp.
		{"allow example.com", "example.com", "1.2.3.4", 0, "", true},

		{"allow example.com:80,443", "example.com", "1.2.3.4", 443, "tcp", true},
		{"allow example.com:80,443", "example.com", "1.2.3.4", 80, "udp", true},
		{"allow example.com:80,443", "example.com", "1.2.3.4", 8080, "tcp", false},

		{"allow example.com/udp", "example.com", "1.2.3.4", 53, "udp", true},
		{"allow example.com/udp", "example.com", "1.2.3.4", 53, "tcp", false},
		{"allow example.com/udp", "example.com", "1.2.3.4", 0, "", false},
		{"allow example.com:53/udp", "example.com", "1.2.3.4", 53, "udp", true},
		{"allow example.com:53/udp", "example.com", "1.2.3.4", 54, "udp", false},
		{"allow example.com:53/udp", "example.com", "1.2.3.4", 53, "tcp", false},

		{"allow 1.2.3.4", "", "1.2.3.4", 443, "tcp", true},
		// The domain doesn't matter for address rules.
		{"allow 1.2.3.4", "example.com", "1.2.3.4", 443, "tcp", true},
		{"allow 1.2.3.4", "", "1.2.3.5", 443, "tcp", false},
		{"allow 1.2.3.4", "", "::ffff:1.2.3.4", 443, "tcp", false},
		{"allow [fd00::1]", "", "fd00::1", 443, "tcp", true},
		{"allow [fd00::1]", "", "fd00::2", 443, "tcp", false},

		{"ask 192.168.0.0/16", "", "192.168.200.1", 443, "tcp", true},
		{"ask 192.168.0.0/16", "", "192.169.0.1", 443, "tcp", false},
		{"ask 192.168.0.0/16", "", "fd00::1", 443, "tcp", false},
		{"allow [fd00::/8]:443", "", "fd12::1", 443, "tcp", true},
		{"allow [fd00::/8]:443", "", "fd12::1", 80, "tcp", false},
		{"allow 10.0.0.0/8:443/tcp", "", "10.1.2.3", 443, "udp", false},
	}
	for _, c := range cases {
		r, err := parseRule(c.rule)
		if err != nil {
			t.Fatalf("parseRule(%q): %v", c.rule, err)
		}
		addr := netip.MustParseAddr(c.addr)
		if got := r.matches(c.domain, addr, c.port, c.proto); got != c.want {
			t.Errorf("%q matches(%q, %s, %d, %q) = %v, want %v",
				c.rule, c.domain, addr, c.port, c.proto, got, c.want)
		}
	}
}

func TestCompareSpecificity(t *testing.T) {
	cases := []struct {
		rule, other string
		want        int
	}{
		// The action doesn't matter.
		{"allow example.com", "deny example.com", 0},
		// Address rules win over domain rules, even a /0 range over a
		// domain with ports and protocol.
		{"allow 1.2.3.4", "deny example.com", 1},
		{"ask 0.0.0.0/0", "allow example.com:443/tcp", 1},
		// A longer prefix wins, regardless of ports and protocol.
		{"allow 1.2.3.4", "deny 1.2.0.0/16:443/tcp", 1},
		{"deny 1.2.0.0/16", "allow 1.2.3.0/24", -1},
		{"allow [fd00::1]", "deny [fd00::/8]:443", 1},
		// Ports win over protocol, which wins over neither.
		{"allow example.com:443", "deny example.com/tcp", 1},
		{"allow example.com:443/tcp", "deny example.com:443", 1},
		{"allow example.com/udp", "deny example.com", 1},
		{"allow 1.2.3.4:22", "deny 1.2.3.4", 1},
		// The number and values of ports and the protocol don't matter.
		{"allow example.com:443", "deny example.com:80,443,8080", 0},
		{"allow example.com:80", "deny example.com:443", 0},
		{"allow example.com/tcp", "deny example.com/udp", 0},
	}
	for _, c := range cases {
		r, err := parseRule(c.rule)
		if err != nil {
			t.Fatalf("parseRule(%q): %v", c.rule, err)
		}
		o, err := parseRule(c.other)
		if err != nil {
			t.Fatalf("parseRule(%q): %v", c.other, err)
		}
		if got := r.compareSpecificity(&o); got != c.want {
			t.Errorf("%q compareSpecificity(%q) = %d, want %d", c.rule, c.other, got, c.want)
		}
		if got := o.compareSpecificity(&r); got != -c.want {
			t.Errorf("%q compareSpecificity(%q) = %d, want %d", c.other, c.rule, got, -c.want)
		}
	}
}
