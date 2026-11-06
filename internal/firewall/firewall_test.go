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

func newTestFirewall(defaultAction, askFallback Action, d dialog) (*Firewall, *stubResolver) {
	resolver := &stubResolver{}
	rules := newEmptyRuleSet(defaultAction)
	fw := &Firewall{rules: rules, resolver: resolver, askFallback: askFallback}
	if d != nil {
		fw.SetDialog(d)
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

// fakeDialog waits in Ask until the test presses a key with Answer,
// or fails with err.
type fakeDialog struct {
	err     error
	answers chan byte
	asked   int
}

func newFakeDialog() *fakeDialog {
	return &fakeDialog{answers: make(chan byte)}
}

// Answer blocks until Ask receives the key.
func (d *fakeDialog) Answer(key byte) {
	d.answers <- key
}

func (d *fakeDialog) Ask(question string, choices []pty.Choice) (pty.Choice, error) {
	d.asked++
	if d.err != nil {
		return pty.Choice{}, d.err
	}
	var key byte
	select {
	case key = <-d.answers:
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
	d := newFakeDialog()
	fw, _ := newTestFirewall(Ask, Deny, d)
	fw.rules.Add(Rule{domain: "allowed.com", action: Allow})
	fw.rules.Add(Rule{domain: "denied.com", action: Deny})
	if lookupDenied(t, fw, "allowed.com.") {
		t.Error("allowed.com denied")
	}
	if !lookupDenied(t, fw, "denied.com.") {
		t.Error("denied.com allowed")
	}
	if d.asked != 0 {
		t.Errorf("asked %d times, want 0", d.asked)
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
		d := newFakeDialog()
		fw, _ := newTestFirewall(Ask, Allow, d)
		go d.Answer(c.key)
		if got := lookupDenied(t, fw, "example.com."); got != c.deny {
			t.Errorf("key %q: denied = %v, want %v", c.key, got, c.deny)
		}
		// The answer is remembered.
		if got := lookupDenied(t, fw, "example.com."); got != c.deny {
			t.Errorf("key %q: second denied = %v, want %v", c.key, got, c.deny)
		}
		if d.asked != 1 {
			t.Errorf("key %q: asked %d times, want 1", c.key, d.asked)
		}
	}
}

func TestLookupNetIPUsesFallbackWithoutDialog(t *testing.T) {
	for _, fallback := range []Action{Allow, Deny} {
		fw, _ := newTestFirewall(Ask, fallback, nil)
		if got := lookupDenied(t, fw, "example.com."); got != (fallback == Deny) {
			t.Errorf("fallback %v: denied = %v", fallback, got)
		}
	}
}

func TestLookupNetIPUsesFallbackOnDialogError(t *testing.T) {
	for _, fallback := range []Action{Allow, Deny} {
		d := newFakeDialog()
		d.err = errors.New("terminal too small")
		fw, _ := newTestFirewall(Ask, fallback, d)
		if got := lookupDenied(t, fw, "example.com."); got != (fallback == Deny) {
			t.Errorf("fallback %v: denied = %v", fallback, got)
		}
		// No answer is remembered, so the next query asks again.
		lookupDenied(t, fw, "example.com.")
		if d.asked != 2 {
			t.Errorf("fallback %v: asked %d times, want 2", fallback, d.asked)
		}
	}
}

func TestLookupNetIPAsksOnceForConcurrentQueries(t *testing.T) {
	results := make([]bool, 5)
	d := newFakeDialog()
	fw, _ := newTestFirewall(Ask, Allow, d)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			results[i] = lookupDenied(t, fw, "example.com.")
		})
	}
	d.Answer('d')
	wg.Wait()
	if d.asked != 1 {
		t.Errorf("asked %d times, want 1", d.asked)
	}
	for i, denied := range results {
		if !denied {
			t.Errorf("query %d allowed", i)
		}
	}
}
