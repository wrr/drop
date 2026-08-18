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

package jailfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wrr/drop/internal/osutil"
)

func TestCwdInSandbox(t *testing.T) {
	fsRoot := t.TempDir()
	if err := osutil.MkdirAll(filepath.Join(fsRoot, "home", "al")); err != nil {
		t.Fatal(err)
	}
	if err := osutil.MkdirAll(filepath.Join(fsRoot, "home", "al", "src")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fsRoot, "home", "al", "notes"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// A directory with no permissions, like the ones Drop mounts over
	// the configured 'blocked_paths'.
	if err := os.Mkdir(filepath.Join(fsRoot, "home", "al", "blocked"), 0000); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name       string
		candidates []string
		want       string
		wantErr    bool
	}{
		{
			name:       "first candidate dir exists",
			candidates: []string{"/home/al/src", "/home/al", "/"},
			want:       "/home/al/src",
		},
		{
			name:       "second candidate dir exists",
			candidates: []string{"/home/al/not-mounted", "/home/al", "/"},
			want:       "/home/al",
		},
		{
			name:       "third cadidate dir exists",
			candidates: []string{"/home/bob/src", "/home/bob", "/"},
			want:       "/",
		},
		{
			name:       "regular file not returned as cwd",
			candidates: []string{"/home/al/notes", "/home/al", "/"},
			want:       "/home/al",
		},
		{
			name:       "dir with no permissions not returned as cwd",
			candidates: []string{"/home/al/blocked", "/home/al", "/"},
			want:       "/home/al",
		},
		{
			name:       "child of a dir with no permissions not returned as cwd",
			candidates: []string{"/home/al/blocked/src", "/home/al", "/"},
			want:       "/home/al",
		},
		{
			name:       "all candidates don't exists",
			candidates: []string{"/home/bob", "/home/al/notes"},
			wantErr:    true,
		},
		{
			name:       "no candidates",
			candidates: nil,
			wantErr:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CwdInSandbox(fsRoot, tc.candidates)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CwdInSandbox(%v) = %q, want an error", tc.candidates, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CwdInSandbox(%v): %v", tc.candidates, err)
			}
			if got != tc.want {
				t.Errorf("CwdInSandbox(%v) = %q, want %q", tc.candidates, got, tc.want)
			}
		})
	}
}

func TestCwdInSandboxMissingFsRoot(t *testing.T) {
	fsRoot := filepath.Join(t.TempDir(), "does-not-exist")
	got, err := CwdInSandbox(fsRoot, []string{"/home/al", "/"})
	if err == nil {
		t.Errorf("CwdInSandbox = %q, want an error", got)
	}
}
