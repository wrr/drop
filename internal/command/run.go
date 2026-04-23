// Copyright 2025-2026 Jan Wrobel <jan@mixedbit.org>
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

// 'drop run ...' command handling

package command

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"runtime/coverage"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"kernel.org/pub/linux/libs/security/libcap/cap"

	"github.com/wrr/drop/internal/cli"
	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/env"
	"github.com/wrr/drop/internal/ipc"
	"github.com/wrr/drop/internal/jailfs"
	"github.com/wrr/drop/internal/netns"
	"github.com/wrr/drop/internal/osutil"
	"github.com/wrr/drop/internal/pty"
)

// RunParent handles parent process logic for the 'drop run' command.
// It executes a child process in a new namespace and the child
// invokes RunChild.
func RunParent(flags *cli.RunFlags, homeDir, dropHome string) error {
	var configPath string
	if !osutil.CanStat(jailfs.EnvPath(dropHome, flags.EnvId)) {
		return fmt.Errorf("environment %q doesn't exist, run 'drop init %v' to create it", flags.EnvId, flags.EnvId)
	}
	if flags.ConfigPath == "" {
		configPath = jailfs.EnvConfigPath(homeDir, flags.EnvId)
	} else {
		configPath = flags.ConfigPath
	}

	cfg, err := config.Read(configPath, homeDir)
	if err != nil {
		return err
	}

	if err := cli.FlagsToConfig(cfg, flags); err != nil {
		return err
	}

	if (len(flags.TcpPublishedPorts) > 0 ||
		len(flags.TcpHostPorts) > 0 ||
		len(flags.UdpPublishedPorts) > 0 ||
		len(flags.UdpHostPorts) > 0) &&
		cfg.Net.Mode != "isolated" {
		return fmt.Errorf("port forwarding is only supported with isolated network mode (--net isolated)")
	}

	// Socket pair for communicating with the child process.
	parentEnd, childEnd, err := ipc.NewParentChildSocket()
	if err != nil {
		return err
	}

	paths, cleanup, err := jailfs.NewPaths(homeDir, flags.EnvId)
	if err != nil {
		return err
	}
	defer cleanup()

	cmd := exec.Command("/proc/self/exe", "-child")
	// 1) If stdin is a terminal, we pass it as-is to the child, so the
	// child is also able to detect that stdin is a terminal. The terminal
	// is then replaced with a new PTY created in the sandbox.
	//
	// 2) If stdin is not a terminal, it is wrapped with io.Reader
	// interface. Such wrapped stdin is no longer os.File and cmd will
	// replace it with a pipe and create a goroutine to read from the
	// io.Reader and write to the pipe. This way, the original file is
	// not passed directly to the sandboxed process, and the sandboxed
	// process cannot access and modify it via /proc/self/fd/0.
	//
	// We then do equivalent wrapping for stdout and stderr.
	if term.IsTerminal(0) {
		cmd.Stdin = os.Stdin
	} else {
		cmd.Stdin = struct{ io.Reader }{os.Stdin}
	}
	if term.IsTerminal(1) {
		cmd.Stdout = os.Stdout
	} else {
		cmd.Stdout = struct{ io.Writer }{os.Stdout}
	}
	if term.IsTerminal(2) {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = struct{ io.Writer }{os.Stderr}
	}
	cmd.ExtraFiles = []*os.File{childEnd.Socket}

	cloneFlags := uintptr(syscall.CLONE_NEWNS |
		syscall.CLONE_NEWIPC |
		syscall.CLONE_NEWPID |
		syscall.CLONE_NEWUSER |
		syscall.CLONE_NEWCGROUP)
	if cfg.Net.Mode != "unjailed" {
		cloneFlags |= syscall.CLONE_NEWNET
	}
	var containerUID, containerGID int
	if flags.BeRoot {
		containerUID = 0
		containerGID = 0
	} else {
		// Keep using current user uid an gid in the jail (so the user is
		// recognized as the same user)
		containerUID = os.Getuid()
		containerGID = os.Getgid()
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: cloneFlags,
		// Code running in a user namespace just after clone() and before
		// execve() system calls has all capabilities in this namespace.
		// This allows such code to setup the namespace with mount()
		// and pivot_root() calls.
		// Unfortunately, GO doesn't allow to execute any user provided
		// code between clone() and execve()
		// (https://github.com/golang/go/issues/12125)
		//
		// The first process executed within the namespace with execve no
		// longer has any capabilities in the namespace (unless it has
		// root uid, but we don't want this). We need to pass required
		// capabilities to this process, so the process can call mount()
		// and pivot_root(), and then we drop these capabilities. For the
		// detailed description of how capabilities are propagated
		// in the user namespace see: man 7 user_namespaces
		//
		// CAP_DAC_OVERRIDE and CAP_FOWNER are needed to mount overlayfs
		//
		// CAP_NET_ADMIN is needed to setup firewall
		AmbientCaps: []uintptr{
			unix.CAP_SYS_ADMIN,
			unix.CAP_DAC_OVERRIDE,
			unix.CAP_FOWNER,
			unix.CAP_NET_ADMIN,
		},
		UidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: containerUID,
				HostID:      os.Getuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: containerGID,
				HostID:      os.Getgid(),
				Size:        1,
			},
		},
		// Disallow a process in the user namespace from dropping/changing
		// group membership: https://lwn.net/Articles/626665/
		GidMappingsEnableSetgroups: false,
		// Kill child process, when this process is killed.
		Pdeathsig: syscall.SIGKILL,
	}

	// Needed to ensure this goroutine is not migrated to a different
	// thread, which is required for correct operation of Pdeathsig.
	// https://github.com/golang/go/issues/27505
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("jailed process start: %v", err)
	}
	defer func() {
		if cmd != nil {
			cmd.Process.Kill()
			cmd.Wait()
			cmd = nil
		}
	}()

	// Catch catchable shutdown signals, so deferred cleanups (jailfs
	// run dir, pasta, PTY restore) run before the parent exits. Forward
	// the signals to the child to make sure it exits before the parent
	// in majority of cases. Pdeathsig above is the safety net to kill
	// the child in case parent dies without terminating the child (for
	// example SIGKILL).
	processTerminated := osutil.CatchAndForwardSignals(cmd.Process)
	defer processTerminated()

	childEnd.Close()

	// Start pasta to provide network connectivity to the jailed process
	// in isolated network mode
	if cfg.Net.Mode == "isolated" {
		cleanPasta, err := netns.StartPasta(cmd.Process.Pid, cfg.Net, paths.Run)
		if err != nil {
			return err
		}
		defer cleanPasta()
	}

	childArgs := ipc.ChildArgs{
		EnvId:    flags.EnvId,
		Paths:    paths,
		Config:   cfg,
		ExecArgs: flags.Args,
	}
	// This must be run after network setup has finished.
	if err := parentEnd.SendChildArgs(childArgs); err != nil {
		return err
	}

	if pty.PtyNeeded() {
		parentPty, err := parentEnd.RecvPty()
		if errors.Is(err, io.EOF) {
			// EOF means child terminated, most likely do to some not socket
			// related problem which will be reported by the child. Continue
			// to cmd.Wait to detect the child termination.
		} else {
			if err != nil {
				return err
			}

			cleanForwardPty, err := pty.ForwardPty(parentPty)
			parentPty = nil // owned by ForwardPty
			if err != nil {
				return err
			}
			defer cleanForwardPty()
		}
	}

	parentEnd.Close()

	err = cmd.Wait()
	cmd = nil
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			// ExitError causes parent process to exit with child's exit
			// code without printing any error message.
			return err
		}
		return fmt.Errorf("jailed process run: %v", err)
	}
	return nil
}

// RunChild handles child process logic for the 'drop run' command.
// It sets up the namespace, drops privileges and executes a command
// provided by the user.
func RunChild() error {
	// The child end of the socket pair is inherited as file descriptor 3
	childEnd := ipc.NewChildEnd(3)
	defer childEnd.Close()

	childArgs, err := childEnd.RecvChildArgs()
	if err != nil {
		return err
	}
	envId := childArgs.EnvId
	paths := childArgs.Paths
	cfg := childArgs.Config
	execArgs := childArgs.ExecArgs

	if err := ensureCapSysAdmin(); err != nil {
		return err
	}

	if _, err := unix.Setsid(); err != nil {
		return fmt.Errorf("setsid for child process: %v", err)
	}

	if err := jailfs.WriteEtcFiles(paths); err != nil {
		return fmt.Errorf("write /etc files: %v", err)
	}

	if err := jailfs.ArrangeFilesystem(paths, cfg); err != nil {
		return err
	}

	// Change working directory to what it was originally, but on the
	// new filesystem root. If Cwd is not accessible on the new
	// filesystem, fallback to home dir and then to /
	chdirPaths := []string{paths.Cwd, paths.HostHome, "/"}
	var chdirErr error
	for _, p := range chdirPaths {
		if chdirErr = unix.Chdir(p); chdirErr == nil {
			break
		}
	}
	if chdirErr != nil {
		return fmt.Errorf("chdir to /: %v", chdirErr)
	}

	if pty.PtyNeeded() {
		parentPty, childPty, err := pty.NewPty()
		if err != nil {
			return err
		}

		if err := childEnd.SendPty(parentPty); err != nil {
			return err
		}
		parentPty.Close()

		if err := pty.SetControllingTerminal(childPty); err != nil {
			return err
		}

		if err := pty.ReplaceTerminal(childPty); err != nil {
			return err
		}
		childPty.Close()
	}

	// Drop all the capabilities in the user namespace.
	//
	// CAP_SYS_ADMIN would allow the user to umount Drop mounts and
	// access the original directories (home dir, proc etc.)
	if err := dropAllCaps(); err != nil {
		return err
	}

	if len(execArgs) == 0 {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		execArgs = []string{shell}
	}

	filteredEnv := env.Filter(os.Environ(), cfg.Environ.ExposedVars)
	envVars := env.SetVars(filteredEnv, cfg.Environ.SetVars, envId)
	path, _ := env.Lookup(envVars, "PATH")
	// Drop config can set or remove PATH, so use the changed value for
	// LookPath. This is not very elegant, would be better to have a
	// version of LookPath that takes envVars or PATH as an argument
	// instead of using PATH from environ.
	if err := os.Setenv("PATH", path); err != nil {
		return fmt.Errorf("set PATH environment variable: %v", err)
	}

	sandboxedProg := execArgs[0]

	// Searches PATH. We do it instead of relying on env, because if
	// PATH is empty, env uses some default for the PATH.
	if _, err := exec.LookPath(sandboxedProg); err != nil {
		return fmt.Errorf("command not found: %v", err)
	}
	// Do not execute the sandboxed binary directly, but let
	// /usr/bin/env execute it. This is to ensure that the executed
	// binary cannot point to Drop executable via /proc/self/exe. This
	// would be dangerous on systems where Drop executable is writable
	// by the current user (/usr/bin/env is root-owned, and drop refuses
	// to run as root, so a sandboxed process cannot overwrite it).
	//
	// See CVE-2019-5736;
	// https://blog.dragonsector.pl/2019/02/cve-2019-5736-escape-from-docker-and.html
	// and
	// https://aws.amazon.com/blogs/compute/anatomy-of-cve-2019-5736-a-runc-container-escape/
	// For explanation how writable /proc/self/exe could be exploited.
	//
	// Drop other characteristics could be sufficient to prevent
	// CVE-2019-5736 style attack, but to be extra sure we don't rely
	// only on them:
	//
	// 1) Linux has a mechanism that prevents a binary from being
	// overwritten as long as there is any running process that uses the
	// binary, so the Drop two process architecture, with continuously
	// running parent process prevents sandboxed child from modifying
	// the `drop` binary. Unfortunately, this is not
	// bulletproof. PR_SET_PDEATHSIG on Linux, guarantees the child
	// process is killed, but the process can still execute some
	// instructions before it happens. If the child attempts to write to
	// /proc/self/exe in a tight loop, and the parent is abruptly killed
	// with SIGKILL (so the signal handler is not run and cannot kill
	// the child before parent terminates), there is a short window in
	// which the child write can succeed (tested to work in
	// practice). This attack is hard to execute, because child does not
	// see and cannot kill the parent, it just needs to loop utilizing
	// full CPU and hope the parent will be killed at some point.
	//
	// 2) Sandboxed processes cannot control files in locations from
	// which dynamic libraries are loaded. I'm not aware how or if at
	// all in such case the /proc/self/exe replacement technique could
	// be executed.
	prog := "/usr/bin/env"
	canOverwrite, err := osutil.CanOverwrite(prog)
	if err != nil {
		return err
	}
	if canOverwrite {
		return fmt.Errorf("%v must not be writable by the current user", prog)
	}

	execArgs = append([]string{"env", "--"}, execArgs...)

	if err := allFdsCloseOnExec(); err != nil {
		return fmt.Errorf("set all open file descriptors to close: %v", err)
	}

	// Since the current process is replaced with Exec, we need
	// to write coverage data manually, Go hooks will not execute.
	writeCoverage()

	// Replace the current process
	if err := unix.Exec(prog, execArgs, envVars); err != nil {
		return fmt.Errorf("exec %s: %v", sandboxedProg, err)
	}

	// Should never be reached
	return fmt.Errorf("exec did not replace the drop process")
}

// ensureCapSysAdmin returns an error if process doesn't have
// CAP_SYS_ADMIN capability. Ubuntu restricts user namespaces creation
// via apparmor profiles, but programs that are not allowed to create
// user namespaces, can in fact create them but without capabilities
// that make the namespace usable (clone system call succeeds, child
// process runs and has different user namespace from it's parent as
// indicated by a different id in /proc/self/ns/user). To detect
// situation when creating a namespace is blocked by app armor
// profile, we test for a presence of CAP_SYS_ADMIN.
func ensureCapSysAdmin() error {
	caps := cap.GetProc()
	hasCap, err := caps.GetFlag(cap.Effective, unix.CAP_SYS_ADMIN)
	if err != nil {
		return fmt.Errorf("check CAP_SYS_ADMIN capability: %v", err)
	}
	if !hasCap {
		return fmt.Errorf("not enough capabilities. Are Linux user namespaces enabled? " +
			"Is Drop allowed to use user namespaces via AppArmor profile?")
	}
	return nil
}

func dropAllCaps() error {
	old := cap.GetProc()
	empty := cap.NewSet()
	if err := empty.SetProc(); err != nil {
		return fmt.Errorf("drop privilege: %q -> %q: %v", old, empty, err)
	}
	now := cap.GetProc()
	if cf, _ := now.Cf(empty); cf != 0 {
		return fmt.Errorf("privileges not fully dropped: have=%q, wanted=%q", now, empty)
	}
	return nil
}

// allFdsCloseOnExec ensures all open file descriptors have O_CLOEXEC
// flag set, so are not inherited by the executed process. ExtraFiles
// argument supported by the Go exec package applies only to Files
// open using the Go API, which always passes O_CLOEXEC flag when
// opening files and then reverts the flag for files on the ExtraFiles
// list. File descriptors passed from the parent process, or file
// descriptors that are created by direct Linux syscalls without
// O_CLOEXEC flag are by convention not closed by Go when new process
// is executed.
func allFdsCloseOnExec() error {
	return unix.CloseRange(3, math.MaxInt32, unix.CLOSE_RANGE_CLOEXEC)
}

func writeCoverage() {
	coverdir := os.Getenv("GOCOVERDIR")
	if coverdir != "" {
		coverage.WriteMetaDir(coverdir)
		coverage.WriteCountersDir(coverdir)
	}
}
