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

// Package gvisor is responsible for executing sandboxed programs with
// an additional layer of isolation provided by a gVisor user-space
// kernel.
//
// gVisor is started via 'runsc' binary which expects OCI container
// JSON config.
//
// Drop continues to create and set up the user namespace with all the
// mounts. This is described as the 'Method 2: Caller-Configured
// Userns' in this document:
// https://gvisor.dev/docs/user_guide/rootless/ and is the gVisor
// integration method used by Docker. This allows to reuse all the
// existing filesystem setup logic and work around gVisor's lack of
// support for overlayfs. Drop does the required mounts, and gVisor
// works with already assembled directory trees.
//
// gVisor is configured to use the host network for connectivity,
// which in this case is a separate network namespace setup by Drop
// with pasta. This allows to reuse all the network namespace setup
// and share networking configuration between gVisor and native paths.
//
//
// gVisor allocates terminal and passes it to Drop via
// -console-socket, but only if all standard file descriptors point to
// a terminal. gVisor does not support allocating terminal only for
// some standard descriptors. For example, it is not possible to pass
// stdin as a pipe to gVisor, but have it allocate terminal for
// stdout/err.
// If 'Terminal: true' is set in JSON spec and we add '-pass-fd 0:0'
// command line argument, the 0 descriptor is duplicated into 1 and 2
// (this is likely a bug).
//
// For this reason, if only some standard descriptors are terminals,
// Drop allocates pty in its own user namespace, passes it to gVisor
// for the terminal descriptors and for non-terminal descriptors
// passes the relevant pipe wrappers. This allows terminal to work
// correctly, but because such terminal is a device from outside of
// the gVisor sandbox, /dev/tty within sandbox does not point to it
// (there is also no /dev/pts/ entry, but due to gVisor issue #13535
// these entries are in general missing).

package gvisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/wrr/drop/internal/jailfs"
	"github.com/wrr/drop/internal/osutil"
)

func Exec(args []string, env []string, terminal bool, paths *jailfs.Paths) error {
	runscPath, err := exec.LookPath("runsc")
	if err != nil {
		return fmt.Errorf("runsc binary for the gVisor runtime not found.\n" +
			"Install gVisor (https://gvisor.dev/docs/user_guide/install/) and ensure\n" +
			"'runsc' is on your PATH, or use the 'native' runtime")
	}
	gvisorDir := filepath.Join(paths.Run, "gvisor")
	bundleDir := filepath.Join(gvisorDir, "bundle")
	fsRootDst := filepath.Join(bundleDir, "root")
	if err := osutil.MkdirAll(fsRootDst); err != nil {
		return err
	}
	bindMounts, err := arrangeRootDir(fsRootDst, paths.FsRoot)
	if err != nil {
		return err
	}
	cwd, err := selectCwd(paths.FsRoot, paths.CwdCandidates())
	if err != nil {
		return err
	}
	spec, err := CreateSpec(ContainerConfig{
		FsRootDst:  fsRootDst,
		FsRootSrc:  paths.FsRoot,
		Args:       args,
		Env:        env,
		Cwd:        cwd,
		UID:        os.Getuid(),
		GID:        os.Getgid(),
		Terminal:   terminal,
		BindMounts: bindMounts,
	})
	if err != nil {
		return err
	}
	configPath := filepath.Join(bundleDir, "config.json")
	if err := os.WriteFile(configPath, []byte(spec), 0600); err != nil {
		return fmt.Errorf("write %s: %v", configPath, err)
	}

	argv := []string{
		"runsc",
		// Use the isolated network namespace Drop already set up (with
		// pasta).
		"--network=host",
		"--ignore-cgroups",
		// Do not use gVisor overlay on top of the mounts, just write to
		// the mounted files and dirs if allowed.
		"--overlay2=none",
		"--directfs=true",
		// Root filesystem is created in the 'run' dir and used by this
		// Drop instance exclusively, so access can be optimized. This
		// optimization doesn't matter much, because the root filesystem
		// contains only mount points and symbolic links.
		"--file-access=exclusive",
		// All bind mounted dirs and files need to be handled in the
		// 'shared' mode because host or other Drop instances can modify
		// the files while this gVisor container is running.
		"--file-access-mounts=shared",
		"--root", gvisorDir,
	}
	if debug_log := os.Getenv("DROP_GVISOR_DEBUG_LOG"); debug_log != "" {
		argv = append(argv, "--debug", "--debug-log", debug_log)
	}
	argv = append(argv, "run", "--bundle", bundleDir)
	if terminal {
		argv = append(argv, "--console-socket", paths.PtySocket)
	}
	argv = append(argv,
		"drop-container",
	)

	if err := unix.Exec(runscPath, argv, env); err != nil {
		return fmt.Errorf("exec runsc: %v", err)
	}

	return nil
}

// selectCwd returns the working directory of the sandboxed process:
// the first of the cwdCandidates that exists as a directory that can
// be entered within the assembled root filesystem (fsRoot).
func selectCwd(fsRoot string, cwdCandidates []string) (string, error) {
	root, err := os.OpenRoot(fsRoot)
	if err != nil {
		return "", fmt.Errorf("open sandbox root %s: %v", fsRoot, err)
	}
	defer root.Close()

	for _, dir := range cwdCandidates {
		name := strings.TrimPrefix(dir, "/") // Root wants a root-relative name
		if name == "" {
			name = "."
		}
		// The trailing "." makes the stat fail if the candidate is not
		// a directory or cannot be entered. Without it a directory
		// mounted with no permissions (see blockEntries) would be
		// selected and gVisor would fail to chdir into it.
		if _, err := root.Stat(name + "/."); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("failed to obtain CWD for the sandbox root %s", fsRoot)
}

// arrangeRootDir builds the gVisor container root in fsRootDst dir so
// that it mirrors fsRootSrc, the root filesystem Drop has already
// assembled.
//
// Ideally Drop would bind mount its whole assembled root (fsRootSrc)
// straight into the container root, but runsc does not support bind
// mounting /. Only subdirectories can be bind mounted, so instead
// arrangeRootDir recreates fsRootSrc's top level in fsRootDst:
//
//   - for each top-level directory it creates an empty directory,
//   - for each top-level regular file it creates an empty file,
//   - for each top-level symlink it recreates an identical symlink.
//
// The empty dirs and files exist only to serve as bind-mount targets (a
// bind mount requires its target to already exist). arrangeRootDir
// returns the root-relative path of each such target as the list of
// bind mounts to add to the gVisor OCI spec, e.g.
// ["/etc", "/tmp", "/home"]. Symlinks resolve within the tree and need
// no mount, so they are not included.
//
// If a top-level entry within fsRootSrc is not readable by the
// current user (for example it is included in the 'blocked_paths'
// list), the function instead creates an equally unreadable empty dir
// or file in fsRootDst and does not return it as a bind-mount target.
// gVisor cannot bind mount an unreadable dir or file, recreating
// it as an unreadable placeholder in the gVisor root achieves the
// same result: the entry stays inaccessible to the sandboxed process.
//
// Only the top level is recreated. The spec bind mounts each top-level
// entry recursively (rbind), so everything nested below comes along and
// appears inside gVisor as ordinary files; there is no need to descend
// into or remount lower levels.
func arrangeRootDir(fsRootDst, fsRootSrc string) ([]string, error) {
	entries, err := os.ReadDir(fsRootSrc)
	if err != nil {
		return nil, fmt.Errorf("read assembled root %s: %v", fsRootSrc, err)
	}
	var bindMounts []string
	for _, entry := range entries {
		srcPath := filepath.Join(fsRootSrc, entry.Name())
		dstPath := filepath.Join(fsRootDst, entry.Name())
		// The entry as seen from the container root, e.g. "/etc".
		rootPath := "/" + entry.Name()

		if rootPath == "/dev" {
			// fsRootSrc contains /dev, but it should not be mounted, gVisor
			// will provide own /dev. The same applies to /proc and /sys,
			// but these are not created in fsRootSrc in gVisor mode.
			continue
		}

		switch {
		case entry.Type()&os.ModeSymlink != 0:
			// Recreate the symlink verbatim.
			target, err := os.Readlink(srcPath)
			if err != nil {
				return nil, fmt.Errorf("read symlink %s: %v", srcPath, err)
			}
			if err := os.Symlink(target, dstPath); err != nil {
				return nil, fmt.Errorf("create symlink %s: %v", dstPath, err)
			}
		case entry.IsDir():
			// A readable dir or file is a bind-mount target, so its content
			// and permissions are irrelevant (the mount hides them). An
			// unreadable dir or file cannot be bind mounted, so it is
			// recreated as an unreadable placeholder and not returned as a
			// bind mount.
			readable := osutil.CanRead(srcPath)
			perm := os.FileMode(0700)
			if !readable {
				perm = 0000
			}
			if err := os.Mkdir(dstPath, perm); err != nil {
				return nil, fmt.Errorf("create dir %s: %v", dstPath, err)
			}
			if readable {
				bindMounts = append(bindMounts, rootPath)
			}
		default:
			readable := osutil.CanRead(srcPath)
			perm := os.FileMode(0600)
			if !readable {
				perm = 0000
			}
			if err := osutil.CreateEmptyFile(dstPath, perm); err != nil {
				return nil, err
			}
			if readable {
				bindMounts = append(bindMounts, rootPath)
			}
		}
	}
	return bindMounts, nil
}
