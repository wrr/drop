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
	"net"
	"net/netip"
	"strings"
	"sync"

	nfqueue "github.com/florianl/go-nfqueue/v2"
	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"

	"github.com/wrr/drop/internal/dns"
	"github.com/wrr/drop/internal/log"
	"github.com/wrr/drop/internal/netns"
)

const queueNum = 0

type Firewall struct {
	rules    *RuleSet
	resolver dns.Resolver
	nft      *nftables.Conn
	stop     func()
	// Held while the user is asked, so only one question is shown at a
	// time and questions for the same target (for example DNS retries)
	// do not result in multiple prompts. Guards dialog.
	askMu sync.Mutex
	// Nil until set with SetDialog.
	dialog dialog
}

// New creates and starts the firewall. Installs nftables rules in the
// network namespace of the process pid, which send the first packet
// of each new connection to a queue. The queue allows each connection
// attempt.
//
// The caller should call the Firewall.Stop() function on program
// termination.
func New(pid int, rules *RuleSet, resolver dns.Resolver) (*Firewall, error) {
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

	if err := serveQueue(ctx, queue); err != nil {
		return nil, err
	}
	success = true
	return &Firewall{rules: rules, resolver: resolver, nft: nft, stop: stop}, nil
}

// SetDialog sets the dialog to ask the user about the Ask rules. Safe
// to call concurrently, meant to be called once.
func (f *Firewall) SetDialog(d dialog) {
	f.askMu.Lock()
	defer f.askMu.Unlock()
	f.dialog = d
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
	if !isValidDomain(domain) {
		return nil, notFound(domainRooted, "invalid domain name")
	}
	if f.shouldDeny(domain) {
		return nil, notFound(domainRooted, "firewall deny")
	}
	// Resolver should get rooted domain, so it doesn't expand it.
	return f.resolver.LookupNetIP(ctx, network, domainRooted)
}

// shouldDeny reports whether a DNS query for the domain should be
// denied, asking the user if the rules say so.
func (f *Firewall) shouldDeny(domain string) bool {
	action := f.rules.ResolverAction(domain)
	if action != Ask {
		return action == Deny
	}
	f.askMu.Lock()
	defer f.askMu.Unlock()
	// Each prompt answer adds a rule, so the same question could have
	// already been answered while waiting for the lock.
	action = f.rules.ResolverAction(domain)
	if action != Ask {
		return action == Deny
	}
	if f.dialog != nil {
		choice, err := f.dialog.Ask(question(domain, netip.Addr{}, 0, ""), promptChoices)
		if err != nil {
			info("prompt: %v", err)
			// TODO: use ask_fallback
			return true
		}
		action = Deny
		if choice.Key == 'a' || choice.Key == 'A' {
			action = Allow
		}
		f.rules.Add(Rule{domain: domain, action: action})
		return action == Deny
	}
	// TODO: use ask_fallback
	return true
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

func serveQueue(ctx context.Context, queue *nfqueue.Nfqueue) error {
	handle := func(a nfqueue.Attribute) int {
		if a.PacketID == nil {
			return 0
		}
		if err := queue.SetVerdict(*a.PacketID, nfqueue.NfAccept); err != nil {
			info("set verdict: %v", err)
		}
		// Keep reading.
		return 0
	}
	// Called for errors reading the queue.
	handleErr := func(err error) int {
		// Stopping the firewall (ctx.Err() != nil) makes the pending
		// read fail; that error is expected and not logged.
		if ctx.Err() == nil {
			info("queue: %v", err)
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

func accept() []expr.Any { return []expr.Any{&expr.Verdict{Kind: expr.VerdictAccept}} }

// exprs concatenates expressions into one rule.
func exprs(parts ...[]expr.Any) []expr.Any {
	var out []expr.Any
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func info(format string, args ...any) {
	log.Info("firewall: "+format, args...)
}
