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

func TestQuestion(t *testing.T) {
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + ".c2.evil.example"
	cases := []struct {
		domain string
		addr   netip.Addr
		port   int
		proto  string
		width  int
		want   []string
	}{
		{"example.com", netip.Addr{}, 0, "", 40,
			[]string{"Allow connections to example.com?"}},
		{"", netip.MustParseAddr("10.0.0.1"), 443, "tcp", 80,
			[]string{"Allow connections to 10.0.0.1? (attempted: port 443/tcp)"}},
		{"www.example.com", netip.Addr{}, 0, "", 30,
			[]string{"Allow connections to", "  www.example.com?"}},
		{"www.example.com", netip.Addr{}, 443, "tcp", 40,
			[]string{"Allow connections to", "  www.example.com?", "  (attempted: port 443/tcp)"}},
		{long, netip.Addr{}, 0, "", 40,
			[]string{"Allow connections to", "  [...]bbbbbbbbbbbbbbbb.c2.evil.example?"}},
	}
	for _, c := range cases {
		got := question(c.domain, c.addr, c.port, c.proto, c.width)
		if !slices.Equal(got, c.want) {
			t.Errorf("question(%q, %v, %d, %q, %d) = %q, want %q",
				c.domain, c.addr, c.port, c.proto, c.width, got, c.want)
		}
		for _, line := range got {
			if utf8.RuneCountInString(line) > c.width {
				t.Errorf("line %q longer than %d", line, c.width)
			}
		}
	}
}
