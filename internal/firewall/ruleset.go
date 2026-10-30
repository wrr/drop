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

	"github.com/wrr/drop/internal/config"
)

// RuleSet holds indexed firewall rules.
type RuleSet struct {
	addrRules map[netip.Addr][]Rule
	// Ranges that cover more than one address.
	prefixRules   []Rule
	domainRules   map[string]*domainRules
	defaultAction Action
}

// NewRuleSet creates a RuleSet as configured by the cfg.
// For now, only inline rules are handled.
func NewRuleSet(cfg *config.Firewall) (*RuleSet, error) {
	defaultAction, err := parseAction(cfg.DefaultAction)
	if err != nil {
		return nil, err
	}
	rules := newEmptyRuleSet(defaultAction)
	for _, rstr := range cfg.Rules {
		rule, err := parseRule(rstr)
		if err != nil {
			return nil, fmt.Errorf("firewall rule %q: %v", rstr, err)
		}
		rules.Add(rule)
	}
	return rules, nil
}

// newEmptyRuleSet returns an empty RuleSet with defaultAction for
// traffic that no rule matches.
func newEmptyRuleSet(defaultAction Action) *RuleSet {
	return &RuleSet{
		addrRules:     make(map[netip.Addr][]Rule),
		domainRules:   make(map[string]*domainRules),
		defaultAction: defaultAction,
	}
}

// Add adds the rule to the set. Of equally specific rules matching the
// same traffic, the one added later is used.
func (rs *RuleSet) Add(r Rule) {
	switch {
	case r.domain != "":
		dr := rs.domainRules[r.domain]
		if dr == nil {
			dr = &domainRules{}
			rs.domainRules[r.domain] = dr
		}
		dr.add(r)
	case r.addr.IsSingleIP():
		addr := r.addr.Addr()
		rs.addrRules[addr] = append(rs.addrRules[addr], r)
	default:
		rs.prefixRules = append(rs.prefixRules, r)
	}
}

type domainRules struct {
	broad  Rule
	narrow []Rule
}

func isBroad(r Rule) bool {
	return !r.hasPorts() && !r.hasProto()
}

func (dr *domainRules) hasBroad() bool {
	return dr.broad.domain != ""
}

// p0 and p1 must be sorted but can contain duplicates.
func arePortsSubset(p0, p1 []int) bool {
	i := 0
	for _, x := range p0 {
		for i < len(p1) && p1[i] < x {
			i++
		}
		if i == len(p1) || p1[i] != x {
			return false
		}
	}
	return true
}

func (dr *domainRules) add(rule Rule) {
	if isBroad(rule) {
		dr.broad = rule
		return
	}
	narrow := []Rule{}
	for _, r := range dr.narrow {
		replaced := rule.compareSpecificity(&r) == 0 &&
			r.proto == rule.proto &&
			arePortsSubset(r.ports, rule.ports)
		if !replaced {
			narrow = append(narrow, r)
		}
	}
	dr.narrow = append(narrow, rule)
}

// ResolverAction returns the action for a DNS query for the domain.
func (rs *RuleSet) ResolverAction(domain string) Action {
	dr := rs.domainRules[domain]
	if dr == nil {
		return rs.defaultAction
	}
	narrowAsk := false
	for _, r := range dr.narrow {
		if r.action == Allow {
			// If any narrow rule allows the domain, DNS lookup is allowed.
			return Allow
		}
		if r.action == Ask {
			// If any narrow rule asks, DNS lookup will ask or be allowed.
			narrowAsk = true
		}
	}
	base := rs.defaultAction
	if dr.hasBroad() {
		base = dr.broad.action
	}
	if narrowAsk && base == Deny {
		return Ask
	}
	return base
}

// ConnectionAction returns the action for a connection to addr and
// port with the proto ("tcp", "udp" or empty for other protocols,
// which match only rules without ports and protocol). domain is the
// lowercase name without the trailing dot that addr was resolved
// from, empty if not known.
func (rs *RuleSet) ConnectionAction(domain string, addr netip.Addr, port int, proto string) Action {
	// Rules never contain IPv4-mapped addresses.
	addr = addr.Unmap()
	bestMatch := func(rules []Rule) *Rule {
		var best *Rule
		for i := range rules {
			r := &rules[i]
			if r.matches(domain, addr, port, proto) && (best == nil || r.compareSpecificity(best) >= 0) {
				best = r
			}
		}
		return best
	}

	if b := bestMatch(rs.addrRules[addr]); b != nil {
		return b.action
	}
	if b := bestMatch(rs.prefixRules); b != nil {
		return b.action
	}

	// Address rules win over domain rules.
	if dr := rs.domainRules[domain]; dr != nil {
		if b := bestMatch(dr.narrow); b != nil {
			return b.action
		}
		if dr.hasBroad() {
			return dr.broad.action
		}
	}
	return rs.defaultAction
}
