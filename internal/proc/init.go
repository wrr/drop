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

package proc

import (
	"fmt"
	"math"
	"os"
	"runtime/coverage"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wrr/drop/internal/env"
	"github.com/wrr/drop/internal/gvisor"
	"github.com/wrr/drop/internal/ipc"
	"github.com/wrr/drop/internal/jailfs"
	"github.com/wrr/drop/internal/osutil"
	"github.com/wrr/drop/internal/pasta"
	"github.com/wrr/drop/internal/pty"
)

const initKillAfter = time.Second

// Init handles init process logic for the 'drop run' command.
// It sets up the namespace, drops privileges and executes a command
// provided by the user.
func Init() error {
	// The receiver's end of the socket pair is inherited as file
	// descriptor 3
	argsReceiver := ipc.NewArgsReceiver(3)
	defer argsReceiver.Close()

	// Recv informs the init process that parent's setup has finished.
	sandboxArgs, err := argsReceiver.Recv()
	if err != nil {
		return err
	}
	envId := sandboxArgs.EnvId
	paths := sandboxArgs.Paths
	cfg := sandboxArgs.Config
	execArgs := sandboxArgs.ExecArgs

	if err := ensureCapSysAdmin(); err != nil {
		return err
	}

	ptyNeeded := pty.PtyNeeded()
	var ptySender *ipc.PtySender
	gVisorPtyAllocate := false
	if ptyNeeded {
		if cfg.IsGvisorRuntime() && pty.AllFdsAreTerminal() {
			// gVisor sets up PTY and sends the socket on its own. See the
			// comment in gvisor.go for the explanation why this is done
			// only if all fds are terminal.
			gVisorPtyAllocate = true
		} else {
			// Must be done before pivot root, so PtySocket is still accessible.
			ptySender, err = ipc.NewPtySender(paths.PtySocket)
			if err != nil {
				return err
			}
		}
	}

	if _, err := unix.Setsid(); err != nil {
		return fmt.Errorf("setsid for init process: %v", err)
	}

	dnsAddr := pasta.DNSForwardAddr
	resolvOpts := ""
	if cfg.Net.IsFirewallEnabled() {
		dnsAddr = "127.0.0.1"
		// Increase resolver timeout to give the user time to answer the
		// firewall interactive prompts.
		resolvOpts = "timeout:30 attempts:3"
	}

	if err := jailfs.WriteResolvConf(paths.RunEtc, dnsAddr, resolvOpts); err != nil {
		return fmt.Errorf("write resolv.conf: %v", err)
	}

	if err := jailfs.ArrangeFilesystem(paths, cfg); err != nil {
		return err
	}

	if ptySender != nil {
		parentPty, childPty, err := pty.NewPty()
		if err != nil {
			return err
		}

		err = ptySender.Send(parentPty)
		ptySender.Close()
		if err != nil {
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

	var fsRoot string
	if cfg.IsGvisorRuntime() {
		fsRoot = paths.FsRoot
	} else {
		// Root is already pivoted.
		fsRoot = "/"
	}
	cwdInSandbox, err := jailfs.CwdInSandbox(fsRoot, paths.CwdCandidates())
	if err != nil {
		return err
	}

	// Change working directory to what it should be on the new
	// filesystem root.
	//
	// gVisor applies the CWD on its own.
	if !cfg.IsGvisorRuntime() {
		if err := unix.Chdir(cwdInSandbox); err != nil {
			return fmt.Errorf("chdir to %s: %v", cwdInSandbox, err)
		}
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

	sandboxedProg := execArgs[0]

	if err := osutil.FindExecutable(sandboxedProg, path, fsRoot, cwdInSandbox); err != nil {
		return err
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
	// binary, so the Drop process architecture, with continuously
	// running main process prevents sandboxed child from modifying
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
	// The protection is not needed with gVisor
	if !cfg.IsGvisorRuntime() {
		canOverwrite, err := osutil.CanOverwrite(prog)
		if err != nil {
			return err
		}
		if canOverwrite {
			return fmt.Errorf("%v must not be writable by the current user", prog)
		}

		execArgs = append([]string{"env", "--"}, execArgs...)
	}

	if err := allFdsCloseOnExec(); err != nil {
		return fmt.Errorf("set all open file descriptors to close: %v", err)
	}

	// Since the current process is replaced with Exec, we need
	// to write coverage data manually, Go hooks will not execute.
	writeCoverage()

	// Replace the current process
	if cfg.IsGvisorRuntime() {
		// With gVisor, we don't call noNewPrivileges(), because when
		// gVisor runs as uid 0, its Gofer process actually needs extra
		// caps to restrict itself. The caps dropping and new privileges
		// blocking is still done, but by gVisor. The sandboxed process
		// that gVisor spawns is configured in the JSON spec to have
		// noCaps and NoNewPrivileges.
		if err := gvisor.Exec(execArgs, envVars, gVisorPtyAllocate, paths, cwdInSandbox); err != nil {
			return fmt.Errorf("gvisor exec %s: %v", sandboxedProg, err)
		}
	} else {
		if err := noNewPrivileges(); err != nil {
			return fmt.Errorf("set no_new_privs: %v", err)
		}
		if err := unix.Exec(prog, execArgs, envVars); err != nil {
			return fmt.Errorf("exec %s: %v", sandboxedProg, err)
		}
	}

	// Should never be reached
	return fmt.Errorf("exec did not replace the drop process")
}

// noNewPrivileges forbids all the child processes from regaining
// capabilities. The blocking is done unconditionally, but it is
// needed only when Drop runs as uid 0 in the sandbox (-r option),
// because otherwise the kernel re-grants the full capability set to a
// uid 0 process on execve. no_new_privs suppresses that re-grant.
func noNewPrivileges() error {
	return unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)
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
