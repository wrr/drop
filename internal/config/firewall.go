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

package config

import (
	"fmt"
	"os"
	"slices"
)

// A feature flag that toggles in-progress, partially implemented
// firewall availability.
const firewallFeatureFlag string = "DROP_INPROGRESS_FIREWALL"

func firewallFeatureFlagOn() bool {
	return os.Getenv(firewallFeatureFlag) != ""
}

// Firewall configures filtering of outgoing connections in the
// isolated network mode.
type Firewall struct {
	// ToggleUnset when no config file sets it, which leaves the firewall
	// enabled if the [net.firewall] table is present.
	Enabled Toggle `toml:"enabled"`
	// An action when no rule matches: "allow", "deny" or "ask".
	DefaultAction string `toml:"default_action"`
	// What to do with if "ask" prompt cannot be shown: "allow" or
	// "deny".
	AskFallback string `toml:"ask_fallback"`
	// Files with rules. Relative paths are resolved relative to the
	// directory where config file which includes the rules is placed.
	RuleFiles []string `toml:"rule_files"`
	// File where "always allow" and "always deny" decisions are
	// saved. Used as a rule file automatically (no need to list it in
	// rule_files).
	SavedRulesFile string `toml:"saved_rules_file"`
	// Inline rules.
	Rules []string `toml:"rules"`
}

func setFirewallDefaults(fw *Firewall) {
	if fw.DefaultAction == "" {
		fw.DefaultAction = "ask"
	}
	if fw.AskFallback == "" {
		fw.AskFallback = "deny"
	}
}

func mergeFirewall(base, sub *Firewall) *Firewall {
	if base == nil {
		return sub
	}
	if sub == nil {
		return base
	}
	merged := *base
	if sub.Enabled != ToggleUnset {
		merged.Enabled = sub.Enabled
	}
	if sub.DefaultAction != "" {
		merged.DefaultAction = sub.DefaultAction
	}
	if sub.AskFallback != "" {
		merged.AskFallback = sub.AskFallback
	}
	if sub.SavedRulesFile != "" {
		merged.SavedRulesFile = sub.SavedRulesFile
	}
	merged.RuleFiles = slices.Concat(base.RuleFiles, sub.RuleFiles)
	merged.Rules = slices.Concat(base.Rules, sub.Rules)
	return &merged
}

func firewallPathsToAbs(fw *Firewall, configDir, homeDir string) error {
	for i, path := range fw.RuleFiles {
		absPath, err := includedFileAbsPath(path, configDir, homeDir)
		if err != nil {
			return fmt.Errorf("rule_files %v", err)
		}
		fw.RuleFiles[i] = absPath
	}
	if fw.SavedRulesFile != "" {
		absPath, err := includedFileAbsPath(fw.SavedRulesFile, configDir, homeDir)
		if err != nil {
			return fmt.Errorf("saved_rules_file %v", err)
		}
		fw.SavedRulesFile = absPath
	}
	return nil
}

func validateFirewall(net Net) error {
	fw := net.Firewall
	if fw == nil {
		return nil
	}
	switch fw.DefaultAction {
	case "allow", "deny", "ask":
	default:
		return fmt.Errorf("invalid firewall default_action '%s': must be 'allow', 'deny' or 'ask'", fw.DefaultAction)
	}
	switch fw.AskFallback {
	case "allow", "deny":
	default:
		return fmt.Errorf("invalid firewall ask_fallback '%s': must be 'allow' or 'deny'", fw.AskFallback)
	}
	return nil
}
