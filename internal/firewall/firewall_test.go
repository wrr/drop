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
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wrr/drop/internal/pty"
)

type stubResolver struct {
	mu       sync.Mutex
	lastHost string
}

func newTestFirewall(defaultAction, askFallback Action, p prompter) (*Firewall, *stubResolver) {
	resolver := &stubResolver{}
	rules := newEmptyRuleSet(defaultAction)
	addrs := newResolvedAddrs()
	fw := &Firewall{rules: rules, resolver: resolver, resolvedAddrs: addrs,
		askFallback: askFallback, log: newFirewallLogger(logAll, slog.New(slog.DiscardHandler))}
	if p != nil {
		fw.SetPrompter(p)
	}
	return fw, resolver
}

func (f *Firewall) filterTestConnection(dst destination) Action {
	return f.filterConnection(dst, f.log.newActionLogger())
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
	// The last question, formatted for 80 columns.
	question []string
}

func newFakePrompter() *fakePrompter {
	return &fakePrompter{answers: make(chan byte)}
}

// Answer blocks until Ask receives the key.
func (p *fakePrompter) Answer(key byte) {
	p.answers <- key
}

func (p *fakePrompter) Ask(formatQuestion func(maxWidth int) []string, choices []pty.Choice) (pty.Choice, error) {
	p.asked++
	p.question = formatQuestion(80)
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
		key       byte
		deny      bool
		wantSaved string
	}{
		{'a', false, ""},
		{'A', false, "allow example.com\n"},
		{'d', true, ""},
		{'D', true, "deny example.com\n"},
	}
	for _, c := range cases {
		p := newFakePrompter()
		fw, _ := newTestFirewall(Ask, Allow, p)
		path := filepath.Join(t.TempDir(), "saved.rules")
		fw.rules.savedRulesFile = path
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
		// Only "always" answers are saved.
		saved, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		if string(saved) != c.wantSaved {
			t.Errorf("key %q: saved %q, want %q", c.key, saved, c.wantSaved)
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
	// Held, so all queries get Ask from the rules and wait for the lock
	// before the first answer adds a rule.
	fw.askMu.Lock()
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			results[i] = lookupDenied(t, fw, "example.com.")
		})
	}
	time.Sleep(100 * time.Millisecond)
	fw.askMu.Unlock()
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

func TestLookupNetIPRecordsAddresses(t *testing.T) {
	fw, _ := newTestFirewall(Allow, Deny, nil)
	addTestRules(t, fw.rules, []string{"deny denied.com"})
	for _, domain := range []string{"a.com.", "b.com.", "a.com.", "denied.com."} {
		lookupDenied(t, fw, domain)
	}
	// Without the trailing dot, each once, in the resolution order. Not
	// the denied one.
	want := []string{"a.com", "b.com"}
	if got := fw.resolvedAddrs.domains(netip.MustParseAddr("192.0.2.1")); !slices.Equal(got, want) {
		t.Errorf("domains = %q, want %q", got, want)
	}
}

var testDst = destination{addr: netip.MustParseAddr("192.0.2.1"), port: 443, proto: "tcp"}

func TestFilterConnectionUsesResolvedDomain(t *testing.T) {
	fw, _ := newTestFirewall(Deny, Deny, nil)
	addTestRules(t, fw.rules, []string{"allow example.com"})
	if got := fw.filterTestConnection(testDst); got != Deny {
		t.Errorf("not resolved: got %v, want deny", got)
	}
	fw.resolvedAddrs.add("example.com", []netip.Addr{testDst.addr})
	if got := fw.filterTestConnection(testDst); got != Allow {
		t.Errorf("resolved for example.com: got %v, want allow", got)
	}
}

func TestFilterConnectionAsks(t *testing.T) {
	cases := []struct {
		key       byte
		want      Action
		wantSaved string
	}{
		{'a', Allow, ""},
		{'A', Allow, "allow 192.0.2.1:443/tcp\n"},
		{'d', Deny, ""},
		{'D', Deny, "deny 192.0.2.1:443/tcp\n"},
	}
	wantQuestion := []string{
		"Allow connections to 192.0.2.1 port 443/tcp?",
		"  Address of: example.com",
	}
	for _, c := range cases {
		p := newFakePrompter()
		fw, _ := newTestFirewall(Deny, Deny, p)
		path := filepath.Join(t.TempDir(), "saved.rules")
		fw.rules.savedRulesFile = path
		// The address rule makes the user decide about an address of an
		// allowed domain.
		addTestRules(t, fw.rules, []string{"allow example.com", "ask 192.0.2.0/24"})
		fw.resolvedAddrs.add("example.com", []netip.Addr{testDst.addr})

		go p.Answer(c.key)
		if got := fw.filterTestConnection(testDst); got != c.want {
			t.Errorf("key %q: got %v, want %v", c.key, got, c.want)
		}
		if !slices.Equal(p.question, wantQuestion) {
			t.Errorf("key %q: question %q, want %q", c.key, p.question, wantQuestion)
		}
		// The answer is remembered for this port and protocol only.
		if got := fw.filterTestConnection(testDst); got != c.want {
			t.Errorf("key %q: second got %v, want %v", c.key, got, c.want)
		}
		otherPort := testDst
		otherPort.port = 80
		go p.Answer('d')
		fw.filterTestConnection(otherPort)
		if p.asked != 2 {
			t.Errorf("key %q: asked %d times, want 2", c.key, p.asked)
		}
		saved, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		if string(saved) != c.wantSaved {
			t.Errorf("key %q: saved %q, want %q", c.key, saved, c.wantSaved)
		}
	}
}

func TestFilterConnectionAsksOnceForConcurrentAttempts(t *testing.T) {
	results := make([]Action, 5)
	p := newFakePrompter()
	fw, _ := newTestFirewall(Ask, Allow, p)
	// Held, so all attempts get Ask from the rules and wait for the lock
	// before the first answer adds a rule.
	fw.askMu.Lock()
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			results[i] = fw.filterTestConnection(testDst)
		})
	}
	time.Sleep(100 * time.Millisecond)
	fw.askMu.Unlock()
	p.Answer('d')
	wg.Wait()
	if p.asked != 1 {
		t.Errorf("asked %d times, want 1", p.asked)
	}
	for i, action := range results {
		if action != Deny {
			t.Errorf("attempt %d: got %v, want deny", i, action)
		}
	}
}

func TestAnswerRule(t *testing.T) {
	cases := []struct {
		dst  destination
		want string
	}{
		{destination{addr: netip.MustParseAddr("192.0.2.1")}, "deny 192.0.2.1"},
		{destination{addr: netip.MustParseAddr("2001:db8::1"), port: 53, proto: "udp"},
			"deny [2001:db8::1]:53/udp"},
	}
	for _, c := range cases {
		if got := answerRule(Deny, c.dst).String(); got != c.want {
			t.Errorf("answerRule(Deny, %s) = %q, want %q", c.dst, got, c.want)
		}
	}
}

// captureLog makes fw log all decisions to the returned builder.
func captureLog(fw *Firewall) *strings.Builder {
	var b strings.Builder
	fw.log = newFirewallLogger(logAll, slog.New(slog.NewTextHandler(&b, nil)))
	return &b
}

func TestLookupNetIPLogged(t *testing.T) {
	cases := []struct {
		name     string
		domain   string
		fallback Action
		p        *fakePrompter
		want     []string
	}{
		{"invalid name", "*.com.", Deny, nil,
			[]string{"msg=dns", `domain=*.com`, "action=deny", `reason="invalid domain name"`}},
		{"denied by rule", "denied.com.", Allow, nil,
			[]string{"domain=denied.com", "action=deny"}},
		{"no prompter", "example.com.", Allow, nil,
			[]string{"action=allow", `reason="ask fallback: prompts unavailable"`}},
		{"prompter error", "example.com.", Deny, &fakePrompter{err: errors.New("terminal size unknown")},
			[]string{"action=deny", `reason="ask fallback: terminal size unknown"`}},
	}
	for _, c := range cases {
		var p prompter
		if c.p != nil {
			p = c.p
		}
		fw, _ := newTestFirewall(Ask, c.fallback, p)
		addTestRules(t, fw.rules, []string{"deny denied.com"})
		out := captureLog(fw)
		lookupDenied(t, fw, c.domain)
		for _, w := range c.want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: log %q does not contain %q", c.name, out, w)
			}
		}
	}
}

func TestPromptAnswerLogged(t *testing.T) {
	p := newFakePrompter()
	fw, _ := newTestFirewall(Ask, Deny, p)
	out := captureLog(fw)
	go p.Answer('A')
	lookupDenied(t, fw, "example.com.")
	// Saving fails without saved_rules_file.
	for _, w := range []string{"prompt=A action=allow", `level=ERROR msg="save firewall rule"`} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("log %q does not contain %q", out, w)
		}
	}
}

func TestFilterConnectionRecordsDestination(t *testing.T) {
	fw, _ := newTestFirewall(Allow, Deny, nil)
	fw.resolvedAddrs.add("a.com", []netip.Addr{testDst.addr})
	fw.resolvedAddrs.add("b.com", []netip.Addr{testDst.addr})
	actionLog := fw.log.newActionLogger()
	fw.filterConnection(testDst, actionLog)
	if actionLog.dst != testDst {
		t.Errorf("dst = %v, want %v", actionLog.dst, testDst)
	}
	if want := []string{"a.com", "b.com"}; !slices.Equal(actionLog.resolvedFrom, want) {
		t.Errorf("resolvedFrom = %q, want %q", actionLog.resolvedFrom, want)
	}
}

func TestFilterPayload(t *testing.T) {
	ports := []byte{0x30, 0x39, 0x01, 0xbb}
	allowed := ipv4Packet(20, unix.IPPROTO_TCP, "192.0.2.1", ports...)
	denied := ipv4Packet(20, unix.IPPROTO_TCP, "198.51.100.1", ports...)
	truncated := ipv4Packet(20, unix.IPPROTO_TCP, "192.0.2.1", 0x30, 0x39)
	cases := []struct {
		name    string
		payload *[]byte
		want    packetVerdict
		wantLog []string
	}{
		{"no payload", nil, verdictDrop,
			[]string{`level=ERROR msg="nfqueue packet without payload"`, "msg=connect action=deny"}},
		{"truncated", &truncated, verdictDrop,
			[]string{`action=deny reason="parse destination: truncated tcp header"`}},
		{"allowed", &allowed, verdictAccept,
			[]string{"msg=connect destination=192.0.2.1:443/tcp action=allow"}},
		{"denied", &denied, verdictReject,
			[]string{"msg=connect destination=198.51.100.1:443/tcp action=deny"}},
	}
	for _, c := range cases {
		fw, _ := newTestFirewall(Allow, Deny, nil)
		addTestRules(t, fw.rules, []string{"deny 198.51.100.1"})
		out := captureLog(fw)
		if got := fw.filterPayload(c.payload); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		for _, w := range c.wantLog {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: log %q does not contain %q", c.name, out, w)
			}
		}
	}
}
