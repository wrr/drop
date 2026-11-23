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

// Package firewall filters sandbox outgoing traffic.
package firewall

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"

	nfqueue "github.com/florianl/go-nfqueue/v2"
	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/dns"
	"github.com/wrr/drop/internal/netns"
)

const queueNum = 0

// The kernel always gives lo index 1 in every network namespace.
const loopbackIfindex = 1

type Firewall struct {
	rules         *RuleSet
	resolver      dns.Resolver
	nft           *nftables.Conn
	resolvedAddrs *resolvedAddrs
	log           *firewallLogger
	stop          func()
	// Used when the user can't be asked, Allow or Deny.
	askFallback Action
	// Held while the user is asked, so only one question is shown at a
	// time and questions for the same target (for example DNS retries)
	// do not result in multiple prompts. Guards prompter.
	askMu sync.Mutex
	// Nil until set with SetPrompter.
	prompter prompter
}

type resolvedAddrs struct {
	// mu guards all the fields below.
	mu            sync.Mutex
	addrToDomains map[netip.Addr][]string
}

func newResolvedAddrs() *resolvedAddrs {
	return &resolvedAddrs{
		addrToDomains: make(map[netip.Addr][]string),
	}
}

// domains returns the domains addr was resolved for, nil if none.
func (t *resolvedAddrs) domains(addr netip.Addr) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	// A copy, so callers can't modify the slice without holding the
	// lock.
	return slices.Clone(t.addrToDomains[addr])
}

func (t *resolvedAddrs) add(domain string, addrs []netip.Addr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, addr := range addrs {
		if !slices.Contains(t.addrToDomains[addr], domain) {
			t.addrToDomains[addr] = append(t.addrToDomains[addr], domain)
		}
	}
}

// New creates and starts the firewall configured by cfg, which filters
// traffic according to the rules. Installs nftables rules in the
// network namespace of the process pid, which send the first packet
// of each new connection to a queue. The queue allows or denies each
// connection attempt according to the rules.
//
// The caller should call the Firewall.Stop() function on program
// termination.
func New(pid int, cfg *config.Firewall, rules *RuleSet, resolver dns.Resolver, log *slog.Logger) (*Firewall, error) {
	askFallback, err := parseAction(cfg.AskFallback)
	if err != nil {
		return nil, err
	}
	logMode, err := parseLogMode(cfg.Log)
	if err != nil {
		return nil, err
	}
	nft, queue, err := openNetfilter(pid)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	success := false
	stop := func() {
		cancel()
		queue.Close()
		nft.CloseLasting()
	}
	defer func() {
		if !success {
			stop()
		}
	}()

	if err := installRules(nft); err != nil {
		return nil, err
	}

	fw := Firewall{
		rules:         rules,
		resolver:      resolver,
		log:           newFirewallLogger(logMode, log),
		nft:           nft,
		resolvedAddrs: newResolvedAddrs(),
		stop:          stop,
		askFallback:   askFallback,
	}
	if err := fw.serveQueue(ctx, queue); err != nil {
		return nil, err
	}
	success = true
	return &fw, nil
}

// SetPrompter sets the prompter to ask the user about the Ask rules. Safe
// to call concurrently, meant to be called once.
func (f *Firewall) SetPrompter(p prompter) {
	f.askMu.Lock()
	defer f.askMu.Unlock()
	f.prompter = p
}

// Stop closes Firewall sockets. After stopping the Firewall existing
// rules continue to apply, no new connections are allowed.
func (f *Firewall) Stop() {
	f.stop()
}

// LookupNetIP resolves address taking into account Firewall filtering
// rules.
func (f *Firewall) LookupNetIP(ctx context.Context, network, domainRooted string) ([]netip.Addr, error) {
	// Within the firewall domains are kept without the trailing dot.
	domain := strings.TrimSuffix(domainRooted, ".")
	actionLog := f.log.newActionLogger()
	actionLog.domain(domain)
	defer actionLog.done()
	if !isValidDomain(domain) {
		e := "invalid domain name"
		actionLog.denyWithReason(e)
		return nil, notFound(domainRooted, e)
	}
	action := f.filterDomain(domain, actionLog)
	actionLog.action(action)
	if action == Deny {
		return nil, notFound(domainRooted, "firewall deny")
	}
	// Resolver should get rooted domain, so it doesn't expand it.
	addrs, err := f.resolver.LookupNetIP(ctx, network, domainRooted)
	if err == nil {
		f.resolvedAddrs.add(domain, addrs)
		f.log.Debug("dns lookup", "domain", domain, "addrs", addrs)
	} else {
		f.log.Debug("dns lookup", "domain", domain, "err", err)
	}
	return addrs, err
}

// filterDomain returns the action for a DNS query, Allow or Deny. If
// the rules require Ask, prompts the user and handles the user
// answer.
func (f *Firewall) filterDomain(domain string, actionLog *actionLogger) Action {
	action := f.rules.ResolverAction(domain)
	if action != Ask {
		return action
	}
	f.askMu.Lock()
	defer f.askMu.Unlock()
	// The same question could have already been answered while waiting
	// for the lock.
	action = f.rules.ResolverAction(domain)
	if action == Ask {
		formatQuestion := func(maxWidth int) []string {
			return resolverQuestion(domain, maxWidth)
		}
		action = f.ask(destination{domain: domain}, formatQuestion, actionLog)
	}
	return action
}

func (f *Firewall) serveQueue(ctx context.Context, queue *nfqueue.Nfqueue) error {
	handle := func(a nfqueue.Attribute) int {
		if a.PacketID == nil {
			return 0
		}
		// Process concurrently, so other packets are not blocked when a
		// firewall prompt is shown.
		go func() {
			verdict := nfqueue.NfDrop
			if f.filterPayload(a.Payload) == Allow {
				verdict = nfqueue.NfAccept
			}
			if err := queue.SetVerdict(*a.PacketID, verdict); err != nil {
				f.log.Error("nfqueue set verdict", "err", err)
			}
		}()
		// Keep reading.
		return 0
	}
	// Called for errors reading the queue.
	handleErr := func(err error) int {
		// Stopping the firewall (ctx.Err() != nil) makes the pending
		// read fail; that error is expected and not logged.
		if ctx.Err() == nil {
			f.log.Error("nfqueue read", "err", err)
		}
		// Keep reading. A transient error should not stop the firewall,
		// which would block all new connections.
		return 0
	}
	if err := queue.RegisterWithErrorFunc(ctx, handle, handleErr); err != nil {
		return fmt.Errorf("register nfqueue handler: %v", err)
	}
	return nil
}

// filterPayload returns the action for a queued packet, Allow or
// Deny.
func (f *Firewall) filterPayload(payload *[]byte) Action {
	actionLog := f.log.newActionLogger()
	defer actionLog.done()
	if payload == nil {
		e := "nfqueue packet without payload"
		f.log.Error(e)
		actionLog.denyWithReason(e)
		return Deny
	}
	dst, err := parseDestination(*payload)
	if err != nil {
		actionLog.denyWithReason(fmt.Sprintf("parse destination: %v", err))
		return Deny
	}
	action := f.filterConnection(dst, actionLog)
	actionLog.action(action)
	return action
}

// filterConnection returns the action for an outgoing connection,
// Allow or Deny. If the rules require Ask, prompts the user and
// handles the user answer.
func (f *Firewall) filterConnection(dst destination, actionLog *actionLogger) Action {
	actionLog.destination(dst)
	domains := f.resolvedAddrs.domains(dst.addr)
	if len(domains) > 0 {
		// Set the last domain that resolved to this address. All
		// the resolved domains are allowed.
		dst.domain = domains[len(domains)-1]
		actionLog.domains(domains)
	}
	action := f.rules.connectionAction(dst)
	if action == Ask {
		f.askMu.Lock()
		defer f.askMu.Unlock()
		// The same question could have already been answered while
		// waiting for the lock.
		action = f.rules.connectionAction(dst)
		if action == Ask {
			formatQuestion := func(maxWidth int) []string {
				return connectionQuestion(dst, domains, maxWidth)
			}
			action = f.ask(dst, formatQuestion, actionLog)
		}
	}
	return action
}

func (f *Firewall) ask(dst destination, formatQuestion func(maxWidth int) []string, actionLog *actionLogger) Action {
	if f.prompter == nil {
		actionLog.setReason("ask fallback: prompts unavailable")
		return f.askFallback
	}
	choice, err := f.prompter.Ask(formatQuestion, promptChoices)
	if err != nil {
		actionLog.setReason("ask fallback: " + err.Error())
		return f.askFallback
	}
	action := Deny
	actionLog.promptChoice(choice)
	if choice.Key == 'a' || choice.Key == 'A' {
		action = Allow
	}
	rule := answerRule(action, dst)
	f.rules.Add(rule)
	if choice.Key == 'A' || choice.Key == 'D' {
		if err := f.rules.Save(rule); err != nil {
			f.log.Error("save firewall rule", "err", err)
		}
	}
	return action
}

// answerRule returns the rule for the user's answer to the question
// about dst. For connections, the rule covers dst's address, port
// and protocol. For DNS queries, the whole domain.
func answerRule(action Action, dst destination) Rule {
	r := Rule{
		action: action,
	}
	if dst.hasAddr() {
		r.addr = netip.PrefixFrom(dst.addr, dst.addr.BitLen())
		if dst.port != 0 {
			r.ports = []int{dst.port}
		}
		r.proto = dst.proto
	} else {
		r.domain = dst.domain
	}
	return r
}

func notFound(domainRooted, reason string) error {
	return &net.DNSError{Err: reason, Name: domainRooted, IsNotFound: true}
}

// openNetfilter opens the nftables and nfqueue connections within the
// network namespace of the process with pid. The caller must close
// them with CloseLasting and Close.
func openNetfilter(pid int) (*nftables.Conn, *nfqueue.Nfqueue, error) {
	var nft *nftables.Conn
	var queue *nfqueue.Nfqueue
	err := netns.Do(pid, func() error {
		var err error
		// AsLasting keeps using the socket instead of creating a new one
		// on each Flush.
		nft, err = nftables.New(nftables.AsLasting())
		if err != nil {
			return fmt.Errorf("nftables connection: %v", err)
		}
		queue, err = nfqueue.Open(&nfqueue.Config{
			NfQueue: queueNum,
			// Enough for the IPv4 or IPv6 header and the ports.
			MaxPacketLen: 128,
			Copymode:     nfqueue.NfQnlCopyPacket,
		})
		if err != nil {
			nft.CloseLasting()
			nft = nil
			return fmt.Errorf("nfqueue connection: %v", err)
		}
		return nil
	})
	return nft, queue, err
}

func installRules(nft *nftables.Conn) error {
	table := nft.AddTable(&nftables.Table{
		Family: nftables.TableFamilyINet,
		Name:   "drop_firewall",
	})
	output := nft.AddChain(&nftables.Chain{
		Name:     "output",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookOutput,
		Priority: nftables.ChainPriorityFilter,
	})
	// Accept packets of connections the queue already allowed, and
	// replies to connections started from outside the sandbox or within it.
	nft.AddRule(&nftables.Rule{
		Table: table,
		Chain: output,
		Exprs: exprs(
			ctStateIn(expr.CtStateBitESTABLISHED|expr.CtStateBitRELATED),
			accept(),
		),
	})
	// Traffic within the sandbox, including queries to the Drop DNS
	// server, is not filtered.
	nft.AddRule(&nftables.Rule{
		Table: table,
		Chain: output,
		Exprs: exprs(oifIs(loopbackIfindex), accept()),
	})
	// Pass everything else to the queue.
	nft.AddRule(&nftables.Rule{
		Table: table,
		Chain: output,
		Exprs: []expr.Any{&expr.Queue{Num: queueNum}},
	})
	if err := nft.Flush(); err != nil {
		return fmt.Errorf("install nftables rules: %v", err)
	}
	return nil
}

// ctStateIn matches if the conntrack state has any of the given bits
// set.
func ctStateIn(states uint32) []expr.Any {
	return []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{
			SourceRegister: 1,
			DestRegister:   1,
			Len:            4,
			Mask:           binaryutil.NativeEndian.PutUint32(states),
			Xor:            binaryutil.NativeEndian.PutUint32(0),
		},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(0)},
	}
}

// oifIs matches if the packet's output interface has the given index.
func oifIs(ifindex uint32) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyOIF, Register: 1},
		&expr.Cmp{
			Op:       expr.CmpOpEq,
			Register: 1,
			Data:     binaryutil.NativeEndian.PutUint32(ifindex),
		},
	}
}

func accept() []expr.Any { return []expr.Any{&expr.Verdict{Kind: expr.VerdictAccept}} }

// exprs concatenates expressions into one rule.
func exprs(parts ...[]expr.Any) []expr.Any {
	var out []expr.Any
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
