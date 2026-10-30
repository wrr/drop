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

import "testing"

func TestFirewallNotParsedWithoutEnvVar(t *testing.T) {
	t.Setenv(firewallFeatureFlag, "")
	files := map[string]string{
		"/config/env.toml": `
[net.firewall]
default_action = "deny"
`,
	}
	_, err := readConfig("/config/env.toml", files)
	if err := checkError("unrecognized key: net.firewall", err); err != nil {
		t.Error(err)
	}
}

func TestFirewall(t *testing.T) {
	tests := []struct {
		name              string
		files             map[string]string
		isFirewallEnabled bool
		firewallIsNil     bool
		defaultAction     string
		askFallback       string
		savedRulesFile    string
		ruleFiles         []string
		rules             []string
		error             string
	}{
		{
			name: "no firewall table",
			files: map[string]string{
				"/config/env.toml": "net.mode = \"isolated\"\n",
			},
			isFirewallEnabled: false,
			firewallIsNil:     true,
		},
		{
			name: "empty table enables the firewall with defaults",
			files: map[string]string{
				"/config/env.toml": "[net.firewall]\n",
			},
			isFirewallEnabled: true,
			defaultAction:     "ask",
			askFallback:       "deny",
		},
		{
			name: "all settings",
			files: map[string]string{
				"/config/env.toml": `
[net.firewall]
enabled = true
default_action = "deny"
ask_fallback = "allow"
rule_files = ["npm.rules", "sub-dir/crate.rules", "~/shared/pip.rules", "/etc/global.rules"]
saved_rules_file = "decisions.rules"
`,
			},
			isFirewallEnabled: true,
			defaultAction:     "deny",
			askFallback:       "allow",
			ruleFiles: []string{
				"/config/npm.rules",
				"/config/sub-dir/crate.rules",
				"/home/alice/shared/pip.rules",
				"/etc/global.rules",
			},
			savedRulesFile: "/config/decisions.rules",
		},
		{
			name: "disabled without removing the config",
			files: map[string]string{
				"/config/env.toml": `
[net.firewall]
enabled = false
default_action = "deny"
`,
			},
			isFirewallEnabled: false,
			defaultAction:     "deny",
			askFallback:       "deny",
		},
		{
			name: "firewall inherited from the base config",
			files: map[string]string{
				"/config/env.toml": `
extends = "base.toml"
`,
				"/config/base.toml": `
[net.firewall]
default_action = "deny"
`,
			},
			isFirewallEnabled: true,
			defaultAction:     "deny",
		},
		{
			name: "environment disables the firewall enabled in the base config",
			files: map[string]string{
				"/config/env.toml": `
extends = "base.toml"
[net.firewall]
enabled = false
`,
				"/config/base.toml": `
[net.firewall]
default_action = "deny"
`,
			},
			isFirewallEnabled: false,
			defaultAction:     "deny",
		},
		{
			name: "table in the environment config does not re-enable the firewall",
			files: map[string]string{
				"/config/env.toml": `
extends = "base.toml"
[net.firewall]
ask_fallback = "allow"
`,
				"/config/base.toml": `
[net.firewall]
enabled = false
`,
			},
			isFirewallEnabled: false,
			askFallback:       "allow",
		},
		{
			name: "firewall settings merging",
			files: map[string]string{
				"/config/env.toml": `
extends = "sub-dir/base.toml"
[net.firewall]
enabled = true
default_action = "deny"
ask_fallback = "allow"
rule_files = ["env.rules"]
rules = ["allow api.example.com:443"]
`,
				"/config/sub-dir/base.toml": `
[net.firewall]
enabled = false
default_action = "ask"
ask_fallback = "deny"
saved_rules_file = "foo.rules"
rule_files = ["npm.rules"]
rules = ["deny telemetry.example.com"]
`,
			},
			isFirewallEnabled: true,
			defaultAction:     "deny",
			askFallback:       "allow",
			ruleFiles: []string{
				"/config/sub-dir/npm.rules",
				"/config/env.rules",
			},
			savedRulesFile: "/config/sub-dir/foo.rules",
			rules: []string{
				"deny telemetry.example.com",
				"allow api.example.com:443",
			},
		},
		{
			name: "disabled in the off net mode",
			files: map[string]string{
				"/config/env.toml": `
net.mode = "off"
[net.firewall]
`,
			},
			isFirewallEnabled: false,
			defaultAction:     "ask",
		},
		{
			name: "saved_rules_file must be normalized",
			files: map[string]string{
				"/config/env.toml": `
[net.firewall]
saved_rules_file = "../decisions.rules"
`,
			},
			error: "saved_rules_file path '../decisions.rules' invalid: path is not normalized",
		},
		{
			name: "rule file path must be normalized",
			files: map[string]string{
				"/config/env.toml": `
[net.firewall]
rule_files = ["../npm.rules"]
`,
			},
			error: "rule_files path '../npm.rules' invalid",
		},
		{
			name: "invalid default_action",
			files: map[string]string{
				"/config/env.toml": `
[net.firewall]
default_action = "prompt"
`,
			},
			error: "invalid firewall default_action 'prompt'",
		},
		{
			name: "invalid ask_fallback",
			files: map[string]string{
				"/config/env.toml": `
[net.firewall]
ask_fallback = "ask"
`,
			},
			error: "invalid firewall ask_fallback 'ask'",
		},
		{
			name: "enabled must be a boolean",
			files: map[string]string{
				"/config/env.toml": `
[net.firewall]
enabled = "no"
`,
			},
			error: "expected true or false, got string",
		},
		{
			name: "unrecognized firewall key",
			files: map[string]string{
				"/config/env.toml": `
[net.firewall]
default = "ask"
`,
			},
			error: "unrecognized key: net.firewall.default",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(firewallFeatureFlag, "1")
			cfg, err := readConfig("/config/env.toml", test.files)
			if err := checkError(test.error, err); err != nil {
				t.Fatal(err)
			}
			if test.error != "" {
				return
			}
			if got := cfg.Net.IsFirewallEnabled(); got != test.isFirewallEnabled {
				t.Fatalf("expected IsFirewallEnabled() %v, got %v", test.isFirewallEnabled, got)
			}
			fw := cfg.Net.Firewall
			if test.firewallIsNil {
				if fw != nil {
					t.Fatal("expected firewall table to be missing")
				}
				return
			}
			if test.defaultAction != "" && fw.DefaultAction != test.defaultAction {
				t.Errorf("expected DefaultAction '%s', got '%s'", test.defaultAction, fw.DefaultAction)
			}
			if test.askFallback != "" && fw.AskFallback != test.askFallback {
				t.Errorf("expected AskFallback '%s', got '%s'", test.askFallback, fw.AskFallback)
			}
			if fw.SavedRulesFile != test.savedRulesFile {
				t.Errorf("expected SavedRulesFile '%s', got '%s'", test.savedRulesFile, fw.SavedRulesFile)
			}
			expectSlicesEqual(t, "RuleFiles", fw.RuleFiles, test.ruleFiles)
			expectSlicesEqual(t, "Rules", fw.Rules, test.rules)
		})
	}
}
