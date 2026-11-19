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
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShorten(t *testing.T) {
	cases := []struct {
		d     string
		width int
		want  string
	}{
		{"example.com", 20, "example.com"},
		{"example.com", 11, "example.com"},
		{"aaaa.bbbb.com", 12, "[...]bbb.com"},
		{"abcdefghijklmnopqrstuvwxyz.evil.example", 21, "[...]xyz.evil.example"},
		{"a." + strings.Repeat("b", 60) + ".com.pl", 20, "[...]bbbbbbbb.com.pl"},
		{"abcdef", 1, "f"},
		{"abcdef", 0, ""},
		{"abcdef", -1, ""},
	}
	for _, c := range cases {
		if got := shorten(c.d, c.width); got != c.want {
			t.Errorf("shorten(%q, %d) = %q, want %q", c.d, c.width, got, c.want)
		}
	}
}

func TestResolverQuestion(t *testing.T) {
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + ".c2.evil.example"
	cases := []struct {
		domain string
		width  int
		want   []string
	}{
		{"example.com", 40,
			[]string{"Allow connections to example.com?"}},
		{"www.example.com", 30,
			[]string{"Allow connections to", "  www.example.com?"}},
		{long, 40,
			[]string{"Allow connections to", "  [...]bbbbbbbbbbbbbbbb.c2.evil.example?"}},
	}
	for _, c := range cases {
		got := resolverQuestion(c.domain, c.width)
		if !slices.Equal(got, c.want) {
			t.Errorf("resolverQuestion(%q, %d) = %q, want %q",
				c.domain, c.width, got, c.want)
		}
		for _, line := range got {
			if utf8.RuneCountInString(line) > c.width {
				t.Errorf("line %q longer than %d", line, c.width)
			}
		}
	}
}

func TestConnectionQuestion(t *testing.T) {
	addr := netip.MustParseAddr("192.0.2.1")
	tcp := destination{addr: addr, port: 443, proto: "tcp"}
	icmp := destination{addr: addr}
	// domains returns n domains, in the order they were resolved.
	domains := func(n int) []string {
		var d []string
		for i := range n {
			d = append(d, fmt.Sprintf("d%d.example", i))
		}
		return d
	}
	const cont = "              "
	cases := []struct {
		name    string
		d       destination
		domains []string
		width   int
		want    []string
	}{
		{"one line", tcp, nil, 80,
			[]string{"Allow connections to 192.0.2.1 port 443/tcp?"}},
		{"ipv6", destination{addr: netip.MustParseAddr("2001:db8::1"), port: 53, proto: "udp"}, nil, 80,
			[]string{"Allow connections to 2001:db8::1 port 53/udp?"}},
		{"no port", icmp, nil, 80,
			[]string{"Allow connections to 192.0.2.1?"}},
		{"wrapped", tcp, nil, 30,
			[]string{"Allow connections to", "  192.0.2.1", "  port 443/tcp?"}},
		{"wrapped no port", icmp, nil, 20,
			[]string{"Allow connections to", "  192.0.2.1?"}},
		{"most recent domain first", tcp, domains(2), 80,
			[]string{
				"Allow connections to 192.0.2.1 port 443/tcp?",
				"  Address of: d1.example",
				cont + "d0.example",
			}},
		{"all 7 domains shown", icmp, domains(7), 80,
			[]string{
				"Allow connections to 192.0.2.1?",
				"  Address of: d6.example",
				cont + "d5.example",
				cont + "d4.example",
				cont + "d3.example",
				cont + "d2.example",
				cont + "d1.example",
				cont + "d0.example",
			}},
		{"more than 7 domains", icmp, domains(8), 80,
			[]string{
				"Allow connections to 192.0.2.1?",
				"  Address of: d7.example",
				cont + "d6.example",
				cont + "d5.example",
				cont + "d4.example",
				cont + "d3.example",
				cont + "d2.example",
				cont + "+2 more",
			}},
		{"long domain shortened", tcp, []string{"abcdefghijklmnopqrstuvwxyz.example"}, 30,
			[]string{
				"Allow connections to",
				"  192.0.2.1",
				"  port 443/tcp?",
				"  Address of: [...]xyz.example",
			}},
	}
	for _, c := range cases {
		got := connectionQuestion(c.d, c.domains, c.width)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		for _, line := range got {
			if utf8.RuneCountInString(line) > c.width {
				t.Errorf("%s: line %q longer than %d", c.name, line, c.width)
			}
		}
	}
}
