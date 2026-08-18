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

package osutil

import (
	"os"
	"path/filepath"
	"testing"
)

// newFsRoot creates a sandbox filesystem root with executables to
// search for.
func newFsRoot(t *testing.T) string {
	t.Helper()
	fsRoot := t.TempDir()

	mkdir := func(dir string) {
		if err := MkdirAll(filepath.Join(fsRoot, dir)); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, perm os.FileMode) {
		if err := os.WriteFile(filepath.Join(fsRoot, name), []byte("x"), perm); err != nil {
			t.Fatal(err)
		}
	}
	symlink := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(fsRoot, name)); err != nil {
			t.Fatal(err)
		}
	}

	mkdir("bin")
	write("bin/prog", 0755)
	write("bin/not-executable", 0644)
	// The execute bits are set, but not for the owner, which is the
	// user running the test, so the file cannot be executed.
	write("bin/not-for-owner", 0011)
	mkdir("bin/dir-not-file")

	// The same name as in /bin, but executable. /bin/not-for-owner
	// must not stop the search.
	mkdir("opt")
	write("opt/not-for-owner", 0755)

	mkdir("usr/bin")
	mkdir("etc/alternatives")
	write("usr/bin/editor.real", 0755)
	symlink("/etc/alternatives/editor", "usr/bin/editor")
	symlink("/usr/bin/editor.real", "etc/alternatives/editor")
	symlink("/usr/bin", "sbin")

	// Symlinks to an executable that exists on the host, but not
	// within the sandbox root.
	symlink("/usr/bin/env", "bin/escaping")
	symlink("../../../../usr/bin/env", "bin/escaping-dotdot")

	mkdir("home/al/bin")
	write("home/al/bin/cwd-prog", 0755)
	write("home/al/local-prog", 0755)

	return fsRoot
}

func TestFindExecutable(t *testing.T) {
	fsRoot := newFsRoot(t)

	for _, tc := range []struct {
		name    string
		file    string
		pathEnv string
		cwd     string
		wantErr bool
	}{
		{
			name:    "found in the first PATH entry",
			file:    "prog",
			pathEnv: "/bin:/usr/bin",
			cwd:     "/",
		},
		{
			name:    "found in a further PATH entry",
			file:    "editor.real",
			pathEnv: "/bin:/usr/bin",
			cwd:     "/",
		},
		{
			name:    "not in any PATH entry",
			file:    "missing",
			pathEnv: "/bin:/usr/bin",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "empty PATH",
			file:    "prog",
			pathEnv: "",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "relative PATH entry is skipped",
			file:    "cwd-prog",
			pathEnv: "bin",
			cwd:     "/home/al",
			wantErr: true,
		},
		{
			name:    "empty PATH entry is skipped",
			file:    "local-prog",
			pathEnv: ":/bin",
			cwd:     "/home/al",
			wantErr: true,
		},
		{
			name:    "not executable by the owner does not end the search",
			file:    "not-for-owner",
			pathEnv: "/bin:/opt",
			cwd:     "/",
		},
		{
			name:    "not executable by the owner and not in a further entry",
			file:    "not-for-owner",
			pathEnv: "/bin",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "no execute bits",
			file:    "not-executable",
			pathEnv: "/bin",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "directory is not an executable",
			file:    "dir-not-file",
			pathEnv: "/bin",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "found behind an absolute symlink chain",
			file:    "editor",
			pathEnv: "/usr/bin",
			cwd:     "/",
		},
		{
			name:    "found in a dir reached by an absolute symlink",
			file:    "editor.real",
			pathEnv: "/sbin",
			cwd:     "/",
		},
		{
			name:    "absolute symlink escaping the sandbox root not followed",
			file:    "escaping",
			pathEnv: "/bin",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "'..' symlink escaping the sandbox root not followed",
			file:    "escaping-dotdot",
			pathEnv: "/bin",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "absolute file name does not use PATH",
			file:    "/usr/bin/editor.real",
			pathEnv: "",
			cwd:     "/",
		},
		{
			name:    "file name with a dir is not searched in PATH",
			file:    "dir/prog",
			pathEnv: "/bin",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "relative file name with / resolved against cwd",
			file:    "bin/cwd-prog",
			pathEnv: "",
			cwd:     "/home/al",
		},
		{
			name:    "dot relative file name resolved against cwd",
			file:    "./local-prog",
			pathEnv: "",
			cwd:     "/home/al",
		},
		// /sbin is a symlink to /usr/bin, so ".." resolves to /usr.
		{
			name:    "'..' is resolved after a symlinked cwd, not lexically",
			file:    "../bin/editor.real",
			pathEnv: "",
			cwd:     "/sbin",
		},
		{
			name:    "'..' from a symlinked cwd does not reach the lexical parent",
			file:    "../bin/prog",
			pathEnv: "",
			cwd:     "/sbin",
			wantErr: true,
		},
		{
			name:    "trailing slash in a relative file name",
			file:    "bin/prog/",
			pathEnv: "",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "trailing slash in an absolute file name",
			file:    "/bin/prog/",
			pathEnv: "",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "'..' in a file name does not escape the sandbox root",
			file:    "../../../../etc/passwd",
			pathEnv: "",
			cwd:     "/home/al",
			wantErr: true,
		},
		{
			name:    "empty file name",
			file:    "",
			pathEnv: "/bin",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "'.' file name",
			file:    ".",
			pathEnv: "/bin",
			cwd:     "/",
			wantErr: true,
		},
		{
			name:    "'..' file name",
			file:    "..",
			pathEnv: "/bin",
			cwd:     "/",
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := FindExecutable(tc.file, tc.pathEnv, fsRoot, tc.cwd)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("FindExecutable(%q, %q, cwd %q) = nil, want an error",
						tc.file, tc.pathEnv, tc.cwd)
				}
				return
			}
			if err != nil {
				t.Fatalf("FindExecutable(%q, %q, cwd %q): %v",
					tc.file, tc.pathEnv, tc.cwd, err)
			}
		})
	}
}

func TestFindExecutableMissingFsRoot(t *testing.T) {
	fsRoot := filepath.Join(t.TempDir(), "does-not-exist")
	if err := FindExecutable("prog", "/bin", fsRoot, "/"); err == nil {
		t.Error("FindExecutable = nil, want an error")
	}
}

func TestAbsolutePath(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    string
		cwd  string
		want string
	}{
		{
			name: "absolute path is not changed",
			p:    "/usr/bin",
			cwd:  "/home/al",
			want: "/usr/bin",
		},
		{
			name: "relative path is resolved against cwd",
			p:    "bin",
			cwd:  "/home/al",
			want: "/home/al/bin",
		},
		{
			name: "relative path is resolved against the root cwd",
			p:    "bin",
			cwd:  "/",
			want: "/bin",
		},
		{
			name: "'.' is not removed",
			p:    "./bin",
			cwd:  "/home/al",
			want: "/home/al/./bin",
		},
		{
			name: "'..' is not removed",
			p:    "../bin",
			cwd:  "/home/al",
			want: "/home/al/../bin",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := absolutePath(tc.p, tc.cwd); got != tc.want {
				t.Errorf("absolutePath(%q, %q) = %q, want %q",
					tc.p, tc.cwd, got, tc.want)
			}
		})
	}
}
