// Copyright 2025 Jan Wrobel <jan@mixedbit.org>
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

package env

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wrr/drop/internal/config"
)

// Filter returns a subset of environment variables configured to be
// exposed by the expose patterns. It supports glob patterns
// (e.g., "LC_*") and exact matches. Returns only the environment
// variables that match the patterns.
func Filter(env []string, expose []string) []string {
	return doFilter(env, expose, true)
}

// SetVars sets and expands envrionment variables configured by the
// user in config.toml. If any of these variables is empty, SetVars
// removes it. The function also sets DROP_ENV variable to
// contain envId - id of the current Drop environment. This can be
// changed by setting DROP_ENV in config.toml to some other value or
// to an empty string.
func SetVars(env []string, varsToSet []config.EnvVar, envId string) []string {
	filterOut := []string{"DROP_ENV"}
	for _, envVar := range varsToSet {
		filterOut = append(filterOut, envVar.Name)
	}
	env = doFilter(env, filterOut, false)

	setDefaultDropEnv := true
	for _, envVar := range varsToSet {
		// Allow the config to remove DROP_ENV or to set it to
		// non-default value.
		if envVar.Name == "DROP_ENV" {
			setDefaultDropEnv = false
		}
		if envVar.Value != "" {
			env = append(env, envVar.Expand(os.Getenv))
		}
	}
	if setDefaultDropEnv {
		env = append(env, "DROP_ENV="+envId)
	}
	return env
}

func doFilter(env []string, patterns []string, keepMatched bool) []string {
	var filtered []string

	for _, envVar := range env {
		varName, _, found := strings.Cut(envVar, "=")
		if !found {
			continue // Malformed
		}

		matched := false
		for _, pattern := range patterns {
			if matches(varName, pattern) {
				matched = true
				break
			}
		}
		if matched == keepMatched {
			filtered = append(filtered, envVar)
		}
	}
	return filtered
}

func matches(varName, pattern string) bool {
	if varName == pattern {
		return true
	}

	matched, _ := filepath.Match(pattern, varName)
	return matched
}
