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
// gVisor passes a pseudo terminal file descriptor via a Unix named
// socket to the Drop parent process.

package gvisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/wrr/drop/internal/jailfs"
	"github.com/wrr/drop/internal/osutil"
)

func Exec(args []string, env []string, ptyNeeded bool, paths *jailfs.Paths) error {
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
	spec, err := CreateSpec(ContainerConfig{
		FsRootDst:  fsRootDst,
		FsRootSrc:  paths.FsRoot,
		Args:       args,
		Env:        env,
		Cwd:        paths.Cwd,
		UID:        os.Getuid(),
		GID:        os.Getgid(),
		Terminal:   ptyNeeded,
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
	if ptyNeeded {
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
			if err := osutil.MkdirAll(dstPath); err != nil {
				return nil, err
			}
			bindMounts = append(bindMounts, rootPath)
		default:
			// Regular file (or any other non-dir, non-symlink entry):
			// The content and permissions are irrelevant as the bind
			// mount hides them.
			if err := osutil.CreateEmptyFile(dstPath, 0600); err != nil {
				return nil, err
			}
			bindMounts = append(bindMounts, rootPath)
		}
	}
	return bindMounts, nil
}
