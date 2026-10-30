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
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

type stubResolver struct {
	lastHost string
}

func (s *stubResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	s.lastHost = host
	return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
}

func TestLookupNetIPPassesValidNameUnchanged(t *testing.T) {
	resolver := &stubResolver{}
	rules := newEmptyRuleSet(Allow)
	fw := &Firewall{rules: rules, resolver: resolver}
	if _, err := fw.LookupNetIP(context.Background(), "ip4", "example.com."); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if resolver.lastHost != "example.com." {
		t.Errorf("resolved %q", resolver.lastHost)
	}
}

func TestLookupNetIPRejectsInvalidName(t *testing.T) {
	resolver := &stubResolver{}
	rules := newEmptyRuleSet(Allow)
	fw := &Firewall{rules: rules, resolver: resolver}
	_, err := fw.LookupNetIP(context.Background(), "ip4", "*.com.")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Errorf("got %v, want a not found DNSError", err)
	}
	if resolver.lastHost != "" {
		t.Errorf("invalid name was resolved: %q", resolver.lastHost)
	}
}

func TestLookupNetIPRejectsDeniedDomain(t *testing.T) {
	resolver := &stubResolver{}
	rules := newTestRuleSet(t, Allow, []string{"deny example.com"})
	fw := &Firewall{rules: rules, resolver: resolver}
	_, err := fw.LookupNetIP(context.Background(), "ip4", "example.com.")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Errorf("got %v, want a not found DNSError", err)
	}
	if resolver.lastHost != "" {
		t.Errorf("denied name was resolved: %q", resolver.lastHost)
	}
}
