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
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// FindExecutable searches for an executable named prog. If the prog
// is relative and contains / (starts with ./, or contains any
// directory name), the current working directory is searched,
// otherwise all absolute entries of pathEnv are searched. Relative
// entries of pathEnv are skipped following gVisor and runc behavior.
//
// Returns nil on success, error otherwise.
//
// All lookups are performed the way a process that has fsRoot as its
// root directory would perform them: absolute symlinks are resolved
// relative to fsRoot and ".." components don't escape fsRoot.
//
// pathEnv uses the PATH environment variable format - directory names
// delimited by ':'.
//
// FindExecutable must be called only after the capabilities are dropped,
// otherwise CAP_DAC_OVERRIDE makes every file look executable.
//
// The function is intended only for producing 'command not found
// error' if executable is not accessible within the sandboxed
// filesystem root. For this error to be consistent between gVisor and
// native runtime, we call FindExecutable before the sandboxed program
// is executed. Potential fsRoot escape in this function does not
// imply fsRoot escape in the actual sandbox. It may result in a
// command being wrongly identifed as executable, but then such
// command won't be accessible and will fail to execute with the
// sandbox activated.
func FindExecutable(prog, pathEnv, fsRoot, cwd string) error {
	if !validateExecutableName(prog) {
		return notFound(prog)
	}

	rootFd, err := unix.Open(
		fsRoot, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open sandbox root %s: %v", fsRoot, err)
	}
	defer unix.Close(rootFd)

	if strings.Contains(prog, "/") {
		if isExecutable(rootFd, absolutePath(prog, cwd)) {
			return nil
		}
		return notFound(prog)
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		// gVisor skips PATH entries that are not absolute. For native
		// runtime to be consistent, we also skip relative PATH entries
		// (this is also what runc does). The gVisor's security rationale
		// for this skipping is a bit blurry ("Relative paths aren't safe,
		// no one should be using them."), PATH passed to the program
		// still contains relative entries, so if we set PATH="."  and
		// execute sh -c 'hello', hello from cwd will run, but if we
		// execute 'hello' directly, it fails to be found.
		if !path.IsAbs(dir) {
			continue
		}
		if isExecutable(rootFd, path.Join(dir, prog)) {
			return nil
		}
	}
	return notFound(prog)
}

// absolutePath returns p as an absolute path within the sandbox
// filesystem root. A relative p is interpreted relative to cwd.
//
// The result is not cleaned. "." and ".." are left for the
// kernel to resolve, because it follows symlinks on the way (a
// removed ".." can name a different directory) and it keeps
// ".." within the sandbox root.
func absolutePath(p, cwd string) string {
	if path.IsAbs(p) {
		return p
	}
	return strings.TrimSuffix(cwd, "/") + "/" + p
}

// isExecutable returns true if path point to a file executable by the
// current user.
func isExecutable(rootFd int, path string) bool {
	path = strings.TrimPrefix(path, "/") // openat2 needs a root-relative name
	if path == "" {
		return false
	}
	how := unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_IN_ROOT | unix.RESOLVE_NO_MAGICLINKS,
	}
	fd, err := unix.Openat2(rootFd, path, &how)
	if err != nil {
		return false
	}
	defer unix.Close(fd)

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return false
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return false
	}
	// In addition to the permission bits, this takes into account the
	// file ownership, ACLs and the noexec mount option.
	//
	// ENOSYS means Eaccess is not available or not implemented.
	// EPERM can be returned by Linux containers employing seccomp.
	// In both cases, fall back to checking the permission bits.
	// (the same way stdlib LookPath does)
	err = unix.Faccessat2(fd, "", unix.X_OK, unix.AT_EMPTY_PATH|unix.AT_EACCESS)
	if err == nil {
		return true
	}
	if err != unix.ENOSYS && err != unix.EPERM {
		return false
	}
	return stat.Mode&0111 != 0
}

func notFound(prog string) error {
	return fmt.Errorf("command not found: %s", prog)
}

// validateExecutableName excludes paths that can't be valid
// executable names. See Go issue #74466 and CVE-2025-47906.
func validateExecutableName(s string) bool {
	switch s {
	case "", ".", "..":
		return false
	}
	return true
}
