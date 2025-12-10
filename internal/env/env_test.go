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

func TestSetDropVars(t *testing.T) {
	tests := []struct {
		name            string
		envIn           []string
		setDebianChroot bool
		envId           string
		envOut          []string
	}{
		{
			name: "both variables set",
			envIn: []string{
				"PATH=/usr/bin",
				"USER=alice",
			},
			setDebianChroot: true,
			envId:           "test-env-id",
			envOut: []string{
				"PATH=/usr/bin",
				"USER=alice",
				"debian_chroot=drop",
				"DROP_ENV=test-env-id",
			},
		},
		{
			name: "only drop env set",
			envIn: []string{
				"PATH=/usr/bin",
				"USER=alice",
			},
			setDebianChroot: false,
			envId:           "foo",
			envOut: []string{
				"PATH=/usr/bin",
				"USER=alice",
				"DROP_ENV=foo",
			},
		},
		{
			name: "overwrite existing variables",
			envIn: []string{
				"PATH=/usr/bin",
				"DROP_ENV=old-env-id",
				"debian_chroot=old-chroot",
				"USER=alice",
			},
			setDebianChroot: true,
			envId:           "new-env-id",
			envOut: []string{
				"PATH=/usr/bin",
				"USER=alice",
				"debian_chroot=drop",
				"DROP_ENV=new-env-id",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SetDropVars(tt.envIn, tt.setDebianChroot, tt.envId)

			sort.Strings(result)
			sort.Strings(tt.envOut)

			if !reflect.DeepEqual(result, tt.envOut) {
				t.Errorf("SetDropVars() = %v, expected %v", result, tt.envOut)
			}
		})
	}
}
