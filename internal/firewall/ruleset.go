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
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"strings"
	"sync"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/osutil"
)

// RuleSet holds indexed firewall rules. Safe for concurrent use.
type RuleSet struct {
	defaultAction  Action
	savedRulesFile string

	// mu guards all the fields below.
	mu        sync.Mutex
	addrRules map[netip.Addr][]Rule
	// Ranges that cover more than one address.
	prefixRules []Rule
	domainRules map[string]Rule
}

// NewRuleSet creates a RuleSet as configured by the cfg.
// For now, only inline rules are handled.
func NewRuleSet(cfg *config.Firewall) (*RuleSet, error) {
	defaultAction, err := parseAction(cfg.DefaultAction)
	if err != nil {
		return nil, err
	}
	rules := newEmptyRuleSet(defaultAction)
	for _, path := range cfg.RuleFiles {
		if err := addRulesFromFile(path, rules); err != nil {
			return nil, err
		}
	}
	for _, rstr := range cfg.Rules {
		if err := addRule(rstr, rules); err != nil {
			return nil, err
		}
	}
	if cfg.SavedRulesFile != "" {
		rules.savedRulesFile = cfg.SavedRulesFile
		err := addRulesFromFile(cfg.SavedRulesFile, rules)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			// Do not create the rules file now, so environments that never
			// add rules do not create empty rules files, but check if the
			// file can be created.
			if !osutil.CanCreate(cfg.SavedRulesFile) {
				return nil, fmt.Errorf("saved rules file %s cannot be created", cfg.SavedRulesFile)
			}
		}
	}
	return rules, nil
}

// addRule parses the rule r and adds it to rules.
func addRule(r string, rules *RuleSet) error {
	rule, err := parseRule(r)
	if err != nil {
		return fmt.Errorf("firewall rule %q: %v", r, err)
	}
	rules.Add(rule)
	return nil
}

// addRulesFromFile parses all rules from a path and adds them to the
// rules.
func addRulesFromFile(path string, rules *RuleSet) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("firewall rule file: %w", err)
	}
	for idx, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if err := addRule(line, rules); err != nil {
			return fmt.Errorf("%s:%d: %v", path, idx+1, err)
		}
	}
	return nil
}

// newEmptyRuleSet returns an empty RuleSet with defaultAction for
// traffic that no rule matches.
func newEmptyRuleSet(defaultAction Action) *RuleSet {
	return &RuleSet{
		addrRules:     make(map[netip.Addr][]Rule),
		domainRules:   make(map[string]Rule),
		defaultAction: defaultAction,
	}
}

// Add adds the rule to the set. Of equally specific rules matching the
// same traffic, the one added later is used.
func (rs *RuleSet) Add(r Rule) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	switch {
	case r.domain != "":
		rs.domainRules[r.domain] = r
	case r.addr.IsSingleIP():
		addr := r.addr.Addr()
		rs.addrRules[addr] = append(rs.addrRules[addr], r)
	default:
		rs.prefixRules = append(rs.prefixRules, r)
	}
}

// Save appends the rule to the saved rules file. Does not add the rule
// to the set.
func (rs *RuleSet) Save(r Rule) error {
	s := r.String()
	if rs.savedRulesFile == "" {
		return fmt.Errorf("cannot save '%s': no saved_rules_file", s)
	}
	f, err := os.OpenFile(rs.savedRulesFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("cannot save '%s': %v", s, err)
	}
	// A single write, so lines appended concurrently by other Drop
	// instances don't interleave.
	_, err = f.Write([]byte(s + "\n"))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("cannot save '%s' to %s: %v", s, rs.savedRulesFile, err)
	}
	return nil
}

// ResolverAction returns the action for a DNS query for the domain.
func (rs *RuleSet) ResolverAction(domain string) Action {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if r, ok := rs.domainRules[domain]; ok {
		return r.action
	}
	return rs.defaultAction
}

// connectionAction returns the action for a connection to d.
// Protocols other than tcp and udp match only address rules without
// ports and protocol.
func (rs *RuleSet) connectionAction(d destination) Action {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	// Rules never contain IPv4-mapped addresses.
	d.addr = d.addr.Unmap()
	bestMatch := func(rules []Rule) *Rule {
		var best *Rule
		for i := range rules {
			r := &rules[i]
			if r.matches(d) && (best == nil || r.compareSpecificity(best) >= 0) {
				best = r
			}
		}
		return best
	}

	if b := bestMatch(rs.addrRules[d.addr]); b != nil {
		return b.action
	}
	if b := bestMatch(rs.prefixRules); b != nil {
		return b.action
	}

	// Address rules take priority over domain rules.
	if r, ok := rs.domainRules[d.domain]; ok {
		return r.action
	}
	return rs.defaultAction
}
