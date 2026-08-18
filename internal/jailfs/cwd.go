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
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

// CwdInSandbox returns the working directory of the sandboxed process:
// the first of the cwdCandidates that exists as a directory that can
// be entered within the assembled root filesystem (fsRoot).
//
// CwdInSandbox must be called only after ArrangeFilesystem has
// assembled fsRoot and capabilities have been dropped.
func CwdInSandbox(fsRoot string, cwdCandidates []string) (string, error) {
	rootFd, err := unix.Open(
		fsRoot, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", fmt.Errorf("open sandbox root %s: %v", fsRoot, err)
	}
	defer unix.Close(rootFd)

	for _, dir := range cwdCandidates {
		if canChdir(rootFd, dir) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("failed to obtain CWD for the sandbox root %s", fsRoot)
}

// canChdir tells if dir can be used as the working directory by a
// process that has rootFd as its root directory.
func canChdir(rootFd int, dir string) bool {
	name := strings.TrimPrefix(dir, "/") // openat2 needs a root-relative name
	if name == "" {
		name = "."
	}
	// The trailing "." makes the open fail if the candidate is not a
	// directory or cannot be entered. Without it a directory mounted
	// with no permissions would be selected, because O_PATH does not
	// check the permissions of the last path component.
	name += "/."

	how := unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_IN_ROOT | unix.RESOLVE_NO_MAGICLINKS,
	}
	fd, err := unix.Openat2(rootFd, name, &how)
	if err != nil {
		return false
	}
	unix.Close(fd)
	return true
}
