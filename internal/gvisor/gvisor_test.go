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

package gvisor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wrr/drop/internal/osutil"
)

func TestArrangeRootDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Top-level directory with nested content that must NOT be recreated.
	if err := osutil.MkdirAll(filepath.Join(src, "etc", "ssl")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "etc", "hostname"), []byte("host"), 0644); err != nil {
		t.Fatal(err)
	}
	// Empty top-level directory.
	if err := osutil.MkdirAll(filepath.Join(src, "home")); err != nil {
		t.Fatal(err)
	}
	// Top-level regular file with content.
	if err := os.WriteFile(filepath.Join(src, "initrd.img"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	// Top-level symlinks pointing within the tree.
	if err := os.Symlink("usr/bin", filepath.Join(src, "bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/sbin", filepath.Join(src, "sbin")); err != nil {
		t.Fatal(err)
	}

	bindMounts, err := arrangeRootDir(dst, src)
	if err != nil {
		t.Fatalf("arrangeRootDir: %v", err)
	}

	// Dirs and files become bind mounts; symlinks do not. ReadDir
	// returns entries sorted by name.
	wantMounts := []string{"/etc", "/home", "/initrd.img"}
	if !reflect.DeepEqual(bindMounts, wantMounts) {
		t.Errorf("bindMounts = %v, want %v", bindMounts, wantMounts)
	}

	// Directory mount targets are recreated empty (nested content is
	// not copied).
	for _, dir := range []string{"etc", "home"} {
		info, err := os.Lstat(filepath.Join(dst, dir))
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
		nested, err := os.ReadDir(filepath.Join(dst, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		if len(nested) != 0 {
			t.Errorf("%s has %d entries, want empty", dir, len(nested))
		}
	}

	// File mount target is recreated empty.
	info, err := os.Lstat(filepath.Join(dst, "initrd.img"))
	if err != nil {
		t.Fatalf("stat initrd.img: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
		t.Errorf("initrd.img mode = %v, want regular file", info.Mode())
	}
	if info.Size() != 0 {
		t.Errorf("initrd.img size = %d, want 0", info.Size())
	}

	// Symlinks are recreated verbatim.
	for name, wantTarget := range map[string]string{"bin": "usr/bin", "sbin": "usr/sbin"} {
		target, err := os.Readlink(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("readlink %s: %v", name, err)
		}
		if target != wantTarget {
			t.Errorf("%s -> %q, want %q", name, target, wantTarget)
		}
	}
}

func TestArrangeRootDirUnreadableEntries(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// A readable dir and file, plus an unreadable dir and file.
	if err := osutil.MkdirAll(filepath.Join(src, "readable-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "readable-file"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(src, "blocked-dir"), 0000); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "blocked-file"), []byte("data"), 0000); err != nil {
		t.Fatal(err)
	}

	bindMounts, err := arrangeRootDir(dst, src)
	if err != nil {
		t.Fatalf("arrangeRootDir: %v", err)
	}

	// Only the readable entries are returned as bind-mount targets.
	wantMounts := []string{"/readable-dir", "/readable-file"}
	if !reflect.DeepEqual(bindMounts, wantMounts) {
		t.Errorf("bindMounts = %v, want %v", bindMounts, wantMounts)
	}

	// The unreadable entries are still recreated as placeholders, but
	// with no permissions.
	for _, name := range []string{"blocked-dir", "blocked-file"} {
		info, err := os.Lstat(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0000 {
			t.Errorf("%s permissions = %o, want 0", name, got)
		}
	}
}

func TestArrangeRootDirMissingSrc(t *testing.T) {
	dst := t.TempDir()
	if _, err := arrangeRootDir(dst, filepath.Join(dst, "does-not-exist")); err == nil {
		t.Error("arrangeRootDir with missing source = nil error, want error")
	}
}

func TestSelectCwd(t *testing.T) {
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
			got, err := selectCwd(fsRoot, tc.candidates)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("selectCwd(%v) = %q, want an error", tc.candidates, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectCwd(%v): %v", tc.candidates, err)
			}
			if got != tc.want {
				t.Errorf("selectCwd(%v) = %q, want %q", tc.candidates, got, tc.want)
			}
		})
	}
}

func TestSelectCwdMissingFsRoot(t *testing.T) {
	fsRoot := filepath.Join(t.TempDir(), "does-not-exist")
	got, err := selectCwd(fsRoot, []string{"/home/al", "/"})
	if err == nil {
		t.Errorf("selectCwd = %q, want an error", got)
	}
}
