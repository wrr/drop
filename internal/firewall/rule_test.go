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
			Rule{action: deny, domain: "telemetry.example.com"}},
		{"allow registry.npmjs.org:21,443",
			Rule{action: allow, domain: "registry.npmjs.org", ports: []int{21, 443}}},
		{"allow example.com:65535",
			Rule{action: allow, domain: "example.com", ports: []int{65535}}},
		{"allow example.com:8443/udp",
			Rule{action: allow, domain: "example.com", ports: []int{8443}, proto: "udp"}},
		{"ask google.com/tcp",
			Rule{action: ask, domain: "google.com", proto: "tcp"}},
		{"allow EXAMPLE.com.",
			Rule{action: allow, domain: "example.com"}},
		{"allow 73.23.75.23:45",
			Rule{action: allow, addr: prefix("73.23.75.23/32"), ports: []int{45}}},
		{"allow [fd00::1]",
			Rule{action: allow, addr: prefix("fd00::1/128")}},
		{"ask 192.168.0.0/16",
			Rule{action: ask, addr: prefix("192.168.0.0/16")}},
		{"allow 10.0.0.0/8:443/udp",
			Rule{action: allow, addr: prefix("10.0.0.0/8"), ports: []int{443}, proto: "udp"}},
		{"allow [fd00::/8]:443",
			Rule{action: allow, addr: prefix("fd00::/8"), ports: []int{443}}},
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
