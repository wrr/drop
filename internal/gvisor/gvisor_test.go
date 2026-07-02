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
)

func TestArrangeRootDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Top-level directory with nested content that must NOT be recreated.
	if err := os.MkdirAll(filepath.Join(src, "etc", "ssl"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "etc", "hostname"), []byte("host"), 0644); err != nil {
		t.Fatal(err)
	}
	// Empty top-level directory.
	if err := os.Mkdir(filepath.Join(src, "home"), 0755); err != nil {
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

func TestArrangeRootDirMissingSrc(t *testing.T) {
	dst := t.TempDir()
	if _, err := arrangeRootDir(dst, filepath.Join(dst, "does-not-exist")); err == nil {
		t.Error("arrangeRootDir with missing source = nil error, want error")
	}
}
