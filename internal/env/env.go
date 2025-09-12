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
	var filtered []string

	for _, envVar := range env {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) < 2 {
			continue // Malformed
		}
		varName := parts[0]

		for _, pattern := range expose {
			if matches(varName, pattern) {
				filtered = append(filtered, envVar)
				break
			}
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
