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
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/wrr/drop/internal/pty"
)

type stubResolver struct {
	mu       sync.Mutex
	lastHost string
}

func newTestFirewall(defaultAction, askFallback Action, p prompter) (*Firewall, *stubResolver) {
	resolver := &stubResolver{}
	rules := newEmptyRuleSet(defaultAction)
	fw := &Firewall{rules: rules, resolver: resolver, askFallback: askFallback}
	if p != nil {
		fw.SetPrompter(p)
	}
	return fw, resolver
}

func (s *stubResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHost = host
	return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
}

func TestLookupNetIPPassesValidNameUnchanged(t *testing.T) {
	fw, resolver := newTestFirewall(Allow, Deny, nil)
	if _, err := fw.LookupNetIP(context.Background(), "ip4", "example.com."); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if resolver.lastHost != "example.com." {
		t.Errorf("resolved %q", resolver.lastHost)
	}
}

func TestLookupNetIPRejectsInvalidName(t *testing.T) {
	fw, resolver := newTestFirewall(Allow, Deny, nil)
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
	fw, resolver := newTestFirewall(Allow, Deny, nil)
	addTestRules(t, fw.rules, []string{"deny example.com"})
	_, err := fw.LookupNetIP(context.Background(), "ip4", "example.com.")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Errorf("got %v, want a not found DNSError", err)
	}
	if resolver.lastHost != "" {
		t.Errorf("denied name was resolved: %q", resolver.lastHost)
	}
}

// fakePrompter waits in Ask until the test presses a key with Answer,
// or fails with err.
type fakePrompter struct {
	err     error
	answers chan byte
	asked   int
}

func newFakePrompter() *fakePrompter {
	return &fakePrompter{answers: make(chan byte)}
}

// Answer blocks until Ask receives the key.
func (p *fakePrompter) Answer(key byte) {
	p.answers <- key
}

func (p *fakePrompter) MaxWidth() int {
	return 80
}

func (p *fakePrompter) Ask(question []string, choices []pty.Choice) (pty.Choice, error) {
	p.asked++
	if p.err != nil {
		return pty.Choice{}, p.err
	}
	var key byte
	select {
	case key = <-p.answers:
	// An unexpected question fails the test instead of blocking it.
	case <-time.After(10 * time.Second):
		return pty.Choice{}, errors.New("not answered")
	}
	for _, c := range choices {
		if c.Key == key {
			return c, nil
		}
	}
	return pty.Choice{}, fmt.Errorf("no choice for key %q", key)
}

func lookupDenied(t *testing.T, fw *Firewall, domain string) bool {
	t.Helper()
	_, err := fw.LookupNetIP(context.Background(), "ip4", domain)
	var dnsErr *net.DNSError
	if err != nil && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
		t.Errorf("unexpected error %v", err)
	}
	return err != nil
}

func TestLookupNetIPAllowAndDenyRules(t *testing.T) {
	p := newFakePrompter()
	fw, _ := newTestFirewall(Ask, Deny, p)
	fw.rules.Add(Rule{domain: "allowed.com", action: Allow})
	fw.rules.Add(Rule{domain: "denied.com", action: Deny})
	if lookupDenied(t, fw, "allowed.com.") {
		t.Error("allowed.com denied")
	}
	if !lookupDenied(t, fw, "denied.com.") {
		t.Error("denied.com allowed")
	}
	if p.asked != 0 {
		t.Errorf("asked %d times, want 0", p.asked)
	}
}

func TestLookupNetIPAsks(t *testing.T) {
	cases := []struct {
		key  byte
		deny bool
	}{
		{'a', false},
		{'A', false},
		{'d', true},
		{'D', true},
	}
	for _, c := range cases {
		p := newFakePrompter()
		fw, _ := newTestFirewall(Ask, Allow, p)
		go p.Answer(c.key)
		if got := lookupDenied(t, fw, "example.com."); got != c.deny {
			t.Errorf("key %q: denied = %v, want %v", c.key, got, c.deny)
		}
		// The answer is remembered.
		if got := lookupDenied(t, fw, "example.com."); got != c.deny {
			t.Errorf("key %q: second denied = %v, want %v", c.key, got, c.deny)
		}
		if p.asked != 1 {
			t.Errorf("key %q: asked %d times, want 1", c.key, p.asked)
		}
	}
}

func TestLookupNetIPUsesFallbackWithoutPrompter(t *testing.T) {
	for _, fallback := range []Action{Allow, Deny} {
		fw, _ := newTestFirewall(Ask, fallback, nil)
		if got := lookupDenied(t, fw, "example.com."); got != (fallback == Deny) {
			t.Errorf("fallback %v: denied = %v", fallback, got)
		}
	}
}

func TestLookupNetIPUsesFallbackOnPrompterError(t *testing.T) {
	for _, fallback := range []Action{Allow, Deny} {
		p := newFakePrompter()
		p.err = errors.New("terminal too small")
		fw, _ := newTestFirewall(Ask, fallback, p)
		if got := lookupDenied(t, fw, "example.com."); got != (fallback == Deny) {
			t.Errorf("fallback %v: denied = %v", fallback, got)
		}
		// No answer is remembered, so the next query asks again.
		lookupDenied(t, fw, "example.com.")
		if p.asked != 2 {
			t.Errorf("fallback %v: asked %d times, want 2", fallback, p.asked)
		}
	}
}

func TestLookupNetIPAsksOnceForConcurrentQueries(t *testing.T) {
	results := make([]bool, 5)
	p := newFakePrompter()
	fw, _ := newTestFirewall(Ask, Allow, p)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			results[i] = lookupDenied(t, fw, "example.com.")
		})
	}
	p.Answer('d')
	wg.Wait()
	if p.asked != 1 {
		t.Errorf("asked %d times, want 1", p.asked)
	}
	for i, denied := range results {
		if !denied {
			t.Errorf("query %d allowed", i)
		}
	}
}
