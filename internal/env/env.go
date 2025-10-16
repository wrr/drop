package env

import (
	"path/filepath"
	"strings"
)

// Filter returns a subset of environment variables configured to be
// exposed by the expose patterns. It supports glob patterns
// (e.g., "LC_*") and exact matches. Returns only the environment
// variables that match the patterns.
func Filter(env []string, expose []string) []string {
	return doFilter(env, expose, true)
}

// SetDropVars sets DROP_ENV environment variable to contain envId -
// id of the current Drop environment. If setDebianChroot is true,
// also sets debian_chroot variable to "drop".
func SetDropVars(env []string, setDebianChroot bool, envId string) []string {
	filterOut := []string{"DROP_ENV"}
	if setDebianChroot {
		filterOut = append(filterOut, "debian_chroot")
	}
	env = doFilter(env, filterOut, false)
	if setDebianChroot {
		env = append(env, "debian_chroot=drop")
	}
	return append(env, "DROP_ENV="+envId)
}

func doFilter(env []string, patterns []string, keepMatched bool) []string {
	var filtered []string

	for _, envVar := range env {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) < 2 {
			continue // Malformed
		}
		varName := parts[0]

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
