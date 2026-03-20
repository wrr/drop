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
	"reflect"
	"sort"
	"testing"

	"github.com/wrr/drop/internal/config"
)

func TestFilter(t *testing.T) {
	tests := []struct {
		name     string
		env      []string
		expose   []string
		expected []string
	}{
		{
			name:     "empty envExpose",
			env:      []string{"HOME=/home/user", "PATH=/usr/bin"},
			expose:   []string{},
			expected: []string{},
		},
		{
			name:     "exact match single pattern",
			env:      []string{"HOME=/home/alice", "PATH=/usr/bin", "SECRET=abc"},
			expose:   []string{"HOME"},
			expected: []string{"HOME=/home/alice"},
		},
		{
			name:     "exact match multiple patterns",
			env:      []string{"HOME=/home/alice", "PATH=/usr/bin", "SECRET=abc", "EDITOR=vim"},
			expose:   []string{"HOME", "EDITOR"},
			expected: []string{"HOME=/home/alice", "EDITOR=vim"},
		},
		{
			name:     "glob pattern LC_*",
			env:      []string{"LC_ALL=C", "LC_TIME=en_US.UTF-8", "LC_ADDRESS=pl_PL.UTF-8", "HOME=/home/alice", "SECRET=abc"},
			expose:   []string{"LC_*"},
			expected: []string{"LC_ALL=C", "LC_TIME=en_US.UTF-8", "LC_ADDRESS=pl_PL.UTF-8"},
		},
		{
			name:     "mixed exact and glob patterns",
			env:      []string{"HOME=/home/alice", "LC_ALL=C", "LC_TIME=en_US.UTF-8", "PATH=/usr/bin", "SECRET=abc"},
			expose:   []string{"HOME", "LC_*", "PATH"},
			expected: []string{"HOME=/home/alice", "LC_ALL=C", "LC_TIME=en_US.UTF-8", "PATH=/usr/bin"},
		},
		{
			name:     "no matches",
			env:      []string{"HOME=/home/alice", "PATH=/usr/bin"},
			expose:   []string{"NONEXISTENT", "FOO_*"},
			expected: []string{},
		},
		{
			name:     "malformed environment variables are skipped",
			env:      []string{"HOME=/home/alice", "MALFORMED", "PATH=/usr/bin"},
			expose:   []string{"HOME", "PATH", "MALFORMED"},
			expected: []string{"HOME=/home/alice", "PATH=/usr/bin"},
		},
		{
			name:     "pattern with special characters",
			env:      []string{"VAR_1=value1", "VAR_2=value2", "OTHER=value"},
			expose:   []string{"VAR_*"},
			expected: []string{"VAR_1=value1", "VAR_2=value2"},
		},
		{
			name:     "empty environment",
			env:      []string{},
			expose:   []string{"HOME", "PATH"},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Filter(tt.env, tt.expose)

			if len(result) == 0 && len(tt.expected) == 0 {
				return
			}

			// Sort both slices for comparison since order doesn't matter
			sort.Strings(result)
			sort.Strings(tt.expected)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Filter() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	tests := []struct {
		name      string
		env       []string
		key       string
		wantValue string
		wantFound bool
	}{
		{
			name:      "key found",
			env:       []string{"HOME=/home/alice", "PATH=/usr/bin"},
			key:       "PATH",
			wantValue: "/usr/bin",
			wantFound: true,
		},
		{
			name:      "key not found",
			env:       []string{"HOME=/home/alice"},
			key:       "PATH",
			wantValue: "",
			wantFound: false,
		},
		{
			name:      "empty env",
			env:       []string{},
			key:       "PATH",
			wantValue: "",
			wantFound: false,
		},
		{
			name:      "empty value",
			env:       []string{"PATH="},
			key:       "PATH",
			wantValue: "",
			wantFound: true,
		},
		{
			name:      "value with equalssign",
			env:       []string{"FOO=bar=baz"},
			key:       "FOO",
			wantValue: "bar=baz",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, found := Lookup(tt.env, tt.key)
			if value != tt.wantValue || found != tt.wantFound {
				t.Errorf("Lookup() = (%q, %v), want (%q, %v)", value, found, tt.wantValue, tt.wantFound)
			}
		})
	}
}

func TestSetVars(t *testing.T) {
	tests := []struct {
		name      string
		envIn     []string
		varsToSet []config.EnvVar
		envId     string
		envOut    []string
	}{
		{
			name: "Add and modify env",
			envIn: []string{
				"PATH=/usr/bin",
				"USER=alice",
				"EDITOR=ed",
			},
			varsToSet: []config.EnvVar{
				config.EnvVar{
					Name:  "PATH",
					Value: "/bin",
				},
				config.EnvVar{
					Name:  "FOO",
					Value: "bar",
				},
				config.EnvVar{
					Name:  "EDITOR",
					Value: "ed",
				},
			},
			envId: "test-env-id",
			envOut: []string{
				"PATH=/bin",
				"USER=alice",
				"FOO=bar",
				"EDITOR=ed",
				"DROP_ENV=test-env-id",
			},
		},
		{
			name: "overwrite DROP_ENV",
			envIn: []string{
				"PATH=/usr/bin",
				"DROP_ENV=old-env-id",
				"USER=alice",
			},
			varsToSet: []config.EnvVar{
				config.EnvVar{
					Name:  "PATH",
					Value: "",
				},
				config.EnvVar{
					Name:  "USER",
					Value: "",
				},
			},

			envId: "new-env-id",
			envOut: []string{
				"DROP_ENV=new-env-id",
			},
		},
		{
			name: "expand variables, remove DROP_ENV_VAR",
			envIn: []string{
				"PATH=/usr/bin",
			},
			varsToSet: []config.EnvVar{
				config.EnvVar{
					Name:  "PATH2",
					Value: "$TEST_VAR/bin:${TEST_VAR}/usr/bin",
				},
				config.EnvVar{
					Name:  "DROP_ENV",
					Value: "",
				},
			},
			envId: "test-env-id",
			envOut: []string{
				"PATH=/usr/bin",
				"PATH2=/test/bin:/test/usr/bin",
			},
		},
	}

	t.Setenv("TEST_VAR", "/test")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SetVars(tt.envIn, tt.varsToSet, tt.envId)

			sort.Strings(result)
			sort.Strings(tt.envOut)

			if !reflect.DeepEqual(result, tt.envOut) {
				t.Errorf("SetDropVars() = %v, expected %v", result, tt.envOut)
			}
		})
	}
}
