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
	"testing"
)

func TestParseRule(t *testing.T) {
	tests := []struct {
		entry    string
		expected Rule
	}{
		{"example.com", Rule{Host: "example.com"}},
		{"Example.COM.", Rule{Host: "example.com"}},
		{"*.example.com", Rule{Host: "example.com", Wildcard: true}},
		{"example.com:8080", Rule{Host: "example.com", Port: 8080}},
		{"*.example.com:22", Rule{Host: "example.com", Wildcard: true, Port: 22}},
		{"192.0.2.1", Rule{Host: "192.0.2.1"}},
		{"192.0.2.1:8443", Rule{Host: "192.0.2.1", Port: 8443}},
		{"2001:db8::1", Rule{Host: "2001:db8::1"}},
		{"[2001:db8::1]", Rule{Host: "2001:db8::1"}},
		{"[2001:db8::1]:443", Rule{Host: "2001:db8::1", Port: 443}},
		{"::ffff:192.0.2.1", Rule{Host: "192.0.2.1"}},
	}
	for _, tc := range tests {
		t.Run(tc.entry, func(t *testing.T) {
			r, err := ParseRule(tc.entry)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r != tc.expected {
				t.Errorf("got %+v, expected %+v", r, tc.expected)
			}
		})
	}
}

func TestParseRuleInvalid(t *testing.T) {
	invalid := []string{
		"",
		"*",
		"*.",
		"foo.*.com",
		"exa mple.com",
		"example..com",
		"https://example.com",
		"example.com/path",
		"example.com:0",
		"example.com:65536",
		"example.com:http",
	}
	for _, entry := range invalid {
		t.Run(entry, func(t *testing.T) {
			if r, err := ParseRule(entry); err == nil {
				t.Errorf("expected error, got %+v", r)
			}
		})
	}
}

func TestParseRuleURLError(t *testing.T) {
	_, err := ParseRule("https://example.com")
	expected := "invalid domain 'https://example.com'"
	if err == nil || err.Error() != expected {
		t.Errorf("expected error %q, got %v", expected, err)
	}
}

func TestAllows(t *testing.T) {
	allow, err := ParseAllowlist([]string{
		"example.com",
		"*.github.com",
		"api.example.org:8443",
		"192.0.2.1:22",
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		host     string
		port     int
		expected bool
	}{
		{"example.com", 443, true},
		{"example.com", 80, true},
		{"EXAMPLE.com.", 443, true},
		{"example.com", 22, false},
		{"www.example.com", 443, false},
		{"notexample.com", 443, false},
		{"api.github.com", 443, true},
		{"a.b.github.com", 80, true},
		{"github.com", 443, false},
		{"evilgithub.com", 443, false},
		{"api.example.org", 8443, true},
		{"api.example.org", 443, false},
		{"192.0.2.1", 22, true},
		{"::ffff:192.0.2.1", 22, true},
		{"192.0.2.1", 443, false},
		{"192.0.2.2", 22, false},
	}
	for _, tc := range tests {
		if got := allow.Allows(tc.host, tc.port); got != tc.expected {
			t.Errorf("Allows(%q, %d) = %v, expected %v", tc.host, tc.port, got, tc.expected)
		}
	}
}
