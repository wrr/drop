package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"runtime/coverage"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"kernel.org/pub/linux/libs/security/libcap/cap"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/env"
	"github.com/wrr/drop/internal/jailfs"
	"github.com/wrr/drop/internal/netns"
	"github.com/wrr/drop/internal/osutil"
)

// stringSlice implements flag.Value interface for repeated string flags
type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type Flags struct {
	envId       string
	configPath  string
	networkMode string

	noCwd  bool
	mounts []string

	// These flags are not currently passed from the parent to the child
	// process, because child does not use them.
	beRoot           bool
	tcpPortsToHost   []string
	tcpPortsFromHost []string
	udpPortsToHost   []string
	udpPortsFromHost []string

	// Internal flags passed only to the child process.
	runDir  string
	homeDir string
}

// toChildArgs constructs command line arguments to be passed to the
// started child process. The child shares most of the arguments with
// the parent, but also accepts internal arguments to specify run and
// home dirs.
//
// All of this would not be needed in C, where forked process would
// simply be able to read copies of the config structs created in the
// parent, but in Go we need to recreate the structs in the child by
// passing appropriate flags.
func (f *Flags) toChildArgs(runDir string, homeDir string) []string {
	// Other flags are not passed because child doesn't use them,
	// perhaps for clarity it would be better to pass all the flags.
	childArgs := []string{
		"-child",
		"-env", f.envId,
		"-config", f.configPath,
		"-run-dir", runDir,
		"-home", homeDir,
	}
	if f.networkMode != "" {
		childArgs = append(childArgs, "-net", f.networkMode)
	}
	if f.noCwd {
		childArgs = append(childArgs, "-no-cwd")
	}
	for _, m := range f.mounts {
		childArgs = append(childArgs, "-mount", m)
	}
	// flag.Args() returns remaining command line arguments not
	// recognized as flags (if any): a command to execute with its flags
	return append(childArgs, flag.Args()...)
}

func parseFlags(defaultConfigPath string, isChild bool) (*Flags, error) {
	if !isChild {
		// print usage only when starting parent process, child process is
		// not started by human.
		flag.Usage = func() {
			envId, err := jailfs.CwdToEnvId()
			defaultEnvId := ""
			if err == nil {
				defaultEnvId = fmt.Sprintf(" (default: %s)", envId)
			}

			fmt.Fprintf(os.Stderr, `Drop limits programs abilities to read and write user's files
Usage: drop [options] [command...]
Options:
  -env, -e value
        Environment ID%s
  -config, -c value
        Path to TOML config file (default: %s)
  -root, -r
        Be root (uid 0) in the jail. Useful for running installation scripts that
        require to be run as root. This option doesn't grant any additional privileges to the jailed
        processes. For convenience, the home dir of a root user is not set to /root, but
        kept as the original home dir.

Mounts related options:
  -no-cwd, -nc
        Ignore cwd.mounts entries from config - do not make the current
        working directory available in the sandbox unless some other mount
        entry exposes the CWD.
  -mount, -m value
        Add a mount to the list of mounts from the TOML config file.
        The flag can be passed multiple times.
        Format: source[:target][:rw]
        Examples: -m /mnt -m /tmp:/host-tmp -m ~/my-project::rw

Networking options:
  -net, -n value
        Network mode: off or isolated
  -tcp-ports-to-host, -t value
        Publish TCP port(s) to the host. Format: [hostIP/]hostPort[:sandboxPort]
  -tcp-ports-from-host, -T value
        Publish TCP port(s) from the host. Format: [hostIP/]hostPort[:sandboxPort]
  -udp-ports-to-host, -u value
        Publish UDP port(s) to the host. Format: [hostIP/]hostPort[:sandboxPort]
  -udp-ports-from-host, -U value
        Publish UDP port(s) from the host. Format: [hostIP/]hostPort[:sandboxPort]

  -help, -h
        Show help
`, defaultEnvId, defaultConfigPath)
		}
	}
	var f Flags
	flag.StringVar(&f.envId, "env", "", "")
	flag.StringVar(&f.envId, "e", "", "")
	flag.StringVar(&f.configPath, "config", "", "")
	flag.StringVar(&f.configPath, "c", "", "")

	flag.BoolVar(&f.noCwd, "no-cwd", false, "")
	flag.BoolVar(&f.noCwd, "nc", false, "")
	flag.Var((*stringSlice)(&f.mounts), "mount", "")
	flag.Var((*stringSlice)(&f.mounts), "m", "")

	flag.BoolVar(&f.beRoot, "root", false, "")
	flag.BoolVar(&f.beRoot, "r", false, "")
	flag.StringVar(&f.networkMode, "net", "", "")
	flag.StringVar(&f.networkMode, "n", "", "")
	flag.Var((*stringSlice)(&f.tcpPortsToHost), "tcp-ports-to-host", "")
	flag.Var((*stringSlice)(&f.tcpPortsToHost), "t", "")
	flag.Var((*stringSlice)(&f.tcpPortsFromHost), "tcp-ports-from-host", "")
	flag.Var((*stringSlice)(&f.tcpPortsFromHost), "T", "")
	flag.Var((*stringSlice)(&f.udpPortsToHost), "udp-ports-to-host", "")
	flag.Var((*stringSlice)(&f.udpPortsToHost), "u", "")
	flag.Var((*stringSlice)(&f.udpPortsFromHost), "udp-ports-from-host", "")
	flag.Var((*stringSlice)(&f.udpPortsFromHost), "U", "")

	if isChild {
		// child only flags constructed by the parent process.
		var child bool
		// Child process always has -child argument
		flag.BoolVar(&child, "child", false, "")
		flag.StringVar(&f.runDir, "run-dir", "", "")
		flag.StringVar(&f.homeDir, "home", "", "")
	}

	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("failed to parse command line: %v", err)
	}
	if isChild {
		// Must be present for child process.
		if f.configPath == "" {
			return nil, fmt.Errorf("child process -c argument missing")
		}
		if f.envId == "" {
			return nil, fmt.Errorf("child process -e argument missing")
		}
	} else {
		if f.configPath == "" {
			f.configPath = defaultConfigPath
		}
		if f.envId == "" {
			envId, err := jailfs.CwdToEnvId()
			if err != nil {
				return nil, err
			}
			f.envId = envId
		}
	}

	if !jailfs.IsEnvIdValid(f.envId) {
		return nil, fmt.Errorf("invalid character in env ID")
	}

	return &f, nil
}

// flagsToConfig modifies cfg from a TOML file with values passed via
// command line flags. Command line flags, when present, take priority
// over the config file. The function validates config after the
// modification.
func flagsToConfig(cfg *config.Config, flags *Flags) error {
	for _, m := range flags.mounts {
		mount, err := config.ParseMountCompact(m)
		if err != nil {
			return fmt.Errorf("command line -mount flag: %v", err)
		}
		cfg.Mounts = append(cfg.Mounts, *mount)
	}

	if flags.networkMode != "" {
		cfg.Net.Mode = flags.networkMode
	}
	if len(flags.tcpPortsToHost) > 0 {
		cfg.Net.TCPPublish = flags.tcpPortsToHost
	}
	if len(flags.tcpPortsFromHost) > 0 {
		cfg.Net.TCPFromHost = flags.tcpPortsFromHost
	}
	if len(flags.udpPortsToHost) > 0 {
		cfg.Net.UDPPublish = flags.udpPortsToHost
	}
	if len(flags.udpPortsFromHost) > 0 {
		cfg.Net.UDPFromHost = flags.udpPortsFromHost
	}
	if flags.noCwd {
		cfg.Cwd.Mounts = nil
	}
	// Validate config again, all errors detected should be related to
	// entries modified by this function, because cfg read from a file
	// and passed to this function was already validated during reading.
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("command line flags: %v", err)
	}
	return nil
}

func main() {
	var exitCode int
	var err error
	if len(os.Args) > 1 && os.Args[1] == "-child" {
		exitCode, err = childProcessEntry()
	} else {
		exitCode, err = parentProcessEntry()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	os.Exit(exitCode)
}

func parentProcessEntry() (int, error) {
	if os.Geteuid() == 0 {
		return 1, fmt.Errorf("drop should not be run as root")
	}

	// Obtain home dir in the parent, because with -r option child we
	// run as root in the namespace, but we don't want to use /root as the
	// home dir.
	homeDir, err := currentUserHomeDir()
	if err != nil {
		return 1, err
	}

	dropHome, err := jailfs.DropHome(homeDir)
	if err != nil {
		return 1, err
	}

	defaultConfigPath := jailfs.DefaultConfigPath(dropHome)

	flags, err := parseFlags(defaultConfigPath, false)
	if err != nil {
		return 1, err
	}

	runDir, cleanRunDir, err := jailfs.NewRunDir(dropHome, flags.envId)
	if err != nil {
		return 1, fmt.Errorf("failed to create run dir: %v", err)
	}
	defer cleanRunDir()

	if flags.configPath == defaultConfigPath && !osutil.Exists(flags.configPath) {
		// configPath points to the default config location, but the
		// config file is missing, write the default config.
		if err := config.WriteDefault(flags.configPath, homeDir); err != nil {
			return 1, fmt.Errorf("failed to create default config at %v: %v", flags.configPath, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote default Drop config to %s\n", flags.configPath)
	}

	cfg, err := config.Read(flags.configPath)
	if err != nil {
		return 1, err
	}

	if err := flagsToConfig(cfg, flags); err != nil {
		return 1, err
	}

	if (len(flags.tcpPortsToHost) > 0 ||
		len(flags.tcpPortsFromHost) > 0 ||
		len(flags.udpPortsToHost) > 0 ||
		len(flags.udpPortsFromHost) > 0) &&
		cfg.Net.Mode != "isolated" {
		return 1, fmt.Errorf("port forwarding is only supported with isolated network mode (-n isolated)")
	}

	// Pipe for synchronizing setup with the child process.
	childEnd, parentEnd, err := os.Pipe()
	if err != nil {
		return 1, fmt.Errorf("failed to create pipe: %v", err)
	}

	// /proc/self/exe would be better, because it handles the case of
	// the current binary being removed
	cmd := exec.Command(os.Args[0], flags.toChildArgs(runDir, homeDir)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{childEnd}

	cloneFlags := uintptr(syscall.CLONE_NEWNS |
		syscall.CLONE_NEWIPC |
		syscall.CLONE_NEWPID |
		syscall.CLONE_NEWUSER |
		syscall.CLONE_NEWCGROUP)
	if cfg.Net.Mode != "unjailed" {
		cloneFlags |= syscall.CLONE_NEWNET
	}
	var containerUID, containerGID int
	if flags.beRoot {
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
		return 1, fmt.Errorf("jailed process start failed: %v", err)
	}
	defer discardChildTermInjection()
	defer func() {
		if cmd != nil {
			cmd.Process.Kill()
			cmd.Wait()
			cmd = nil
		}
	}()

	childEnd.Close()

	// Start pasta to provide network connectivity to the jailed process
	// in isolated network mode
	if cfg.Net.Mode == "isolated" {
		cleanPasta, err := netns.StartPasta(cmd.Process.Pid, cfg.Net, runDir)
		if err != nil {
			return 1, err
		}
		defer cleanPasta()
	}

	// Signal child process that setup is finished. This needs to be
	// done when pasta is running, because only then the child can
	// successfully run programs that use network.
	if err := signalParentReady(parentEnd); err != nil {
		return 1, err
	}

	err = cmd.Wait()
	cmd = nil
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode := exitError.ExitCode()
			// Propage exit code of the child
			return exitCode, nil
		}
		return 1, fmt.Errorf("jailed process failed to run: %v", err)
	}
	return 0, nil
}

func childProcessEntry() (int, error) {
	flags, err := parseFlags("", true)
	if err != nil {
		return 1, err
	}

	// Wait for parent to signal that it has finished setup.
	// The read end of the pipe is inherited as file descriptor 3
	readEnd := os.NewFile(3, "parent-ready-pipe")
	if readEnd == nil {
		return 1, fmt.Errorf("failed to get pipe to parent")
	}
	if err := waitParentReady(readEnd); err != nil {
		return 1, err
	}

	if err := ensureCapSysAdmin(); err != nil {
		return 1, err
	}

	paths, err := jailfs.NewPaths(flags.envId, flags.homeDir, flags.runDir)
	if err != nil {
		return 1, err
	}

	cfg, err := config.Read(flags.configPath)
	if err != nil {
		return 1, err
	}

	if err := flagsToConfig(cfg, flags); err != nil {
		return 1, err
	}

	if err := jailfs.WriteEtcFiles(paths); err != nil {
		return 1, fmt.Errorf("failed to write /etc files: %v", err)
	}

	if err := jailfs.ArrangeFilesystem(paths, cfg); err != nil {
		return 1, err
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
		return 1, fmt.Errorf("failed to chdir to /: %v", chdirErr)
	}

	// Drop all the capabilities in the user namespace.
	//
	// CAP_SYS_ADMIN would allow the user to umount Drop mounts and
	// access the original directories (home dir, proc etc.)
	if err := dropAllCaps(); err != nil {
		return 1, err
	}

	progWithArgs := flag.Args()
	if len(progWithArgs) == 0 {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		progWithArgs = []string{shell}
	}

	// Filter environment variables, then add DROP_ENV and debian_chroot
	filteredEnv := env.Filter(os.Environ(), cfg.ExposedEnvVars)
	envVars := env.SetDropVars(filteredEnv, osutil.IsDebianBased(), flags.envId)
	prog, err := exec.LookPath(progWithArgs[0]) // Searches PATH
	if err != nil {
		return 1, fmt.Errorf("command not found: %v", err)
	}

	if err := AllFdsCloseOnExec(); err != nil {
		return 1, fmt.Errorf("failed to set open file descriptors to close: %v", err)
	}

	// Since the current process is replaced with Exec, we need
	// to write coverage data manually, Go hooks will not execute.
	if err := writeCoverage(); err != nil {
		return 1, err
	}

	// Replace the current process
	if err := unix.Exec(prog, progWithArgs, envVars); err != nil {
		return 1, fmt.Errorf("exec %s failed: %v", progWithArgs[0], err)
	}

	// Should never be reached
	return 3, fmt.Errorf("exec failed")
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
		return fmt.Errorf("failed to check CAP_SYS_ADMIN capability: %v", err)
	}
	if !hasCap {
		return fmt.Errorf("not enough capabilities. Are Linux user namespaces enabled? " +
			"Is Drop allowed to use user namespaces via AppArmor profile?")
	}
	return nil
}

// signalParentReady writes to the pipe to signal that parent setup has finished
func signalParentReady(parentEnd *os.File) error {
	defer parentEnd.Close()
	if _, err := parentEnd.Write([]byte{1}); err != nil {
		return fmt.Errorf("failed to write to network ready pipe: %v", err)
	}
	return nil
}

// waitParentReady blocks until the parent signals the setup has finished
func waitParentReady(childEnd *os.File) error {
	defer childEnd.Close()
	buf := make([]byte, 1)
	if _, err := childEnd.Read(buf); err != nil {
		return fmt.Errorf("failed to read from pipe: %v", err)
	}
	return nil
}

// discardChildTermInjection checks if any input is pending on the
// parent standard input and discards it.
//
// This is to prevent the terminating sanboxed process from injecting
// terminal input to the parent, thus executing code outside of the
// sandbox.
//
// https://www.errno.fr/TTYPushback.html
// https://www.openwall.com/lists/oss-security/2023/03/14/2
//
// Note that since kernel 6.2 'sysctl dev.tty.legacy_tiocsti=0'
// (default on Ubuntu 24) disables the ioctl TIOCSTI call, which fixes
// the issue addressed by discardChildTermInjection
func discardChildTermInjection() {
	for {
		// If the child (process 1 in the pid namespace) terminates, all
		// other process in the namespace are killed and the namespace is
		// removed (see man pid_namespaces). Based on this, we assume that
		// when wait() syscall returns, there can be no background
		// processes left running in the namespace that could write to
		// parent input terminal with some delay. We just discard whatever
		// is available after wait() returns.

		readfds := &unix.FdSet{}
		const stdin int = 0
		readfds.Zero()
		readfds.Set(stdin)

		fd_count, err := unix.Select(stdin+1, readfds, nil, nil, &unix.Timeval{Sec: 0, Usec: 0})
		if err != nil {
			fmt.Printf("Failed to discard jailed process stdin leftowers: %v", err)
			return
		}
		if fd_count == 0 {
			// Nothing available
			return
		}
		buf := make([]byte, 128)
		n, err := unix.Read(stdin, buf)
		if err != nil {
			fmt.Printf("Failed to discard jailed process stdin leftowers: %v", err)
			return
		}
		if n == 0 {
			// EOF
			return
		}
		fmt.Fprintf(os.Stderr, "Discarding %d chars\n", n)
	}
}

func dropAllCaps() error {
	old := cap.GetProc()
	empty := cap.NewSet()
	if err := empty.SetProc(); err != nil {
		return fmt.Errorf("failed to drop privilege: %q -> %q: %v", old, empty, err)
	}
	now := cap.GetProc()
	if cf, _ := now.Cf(empty); cf != 0 {
		return fmt.Errorf("failed to fully drop privilege: have=%q, wanted=%q", now, empty)
	}
	return nil
}

func currentUserHomeDir() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %v", err)
	}
	return currentUser.HomeDir, nil
}

// AllFdsCloseOnExec ensures all open file descriptors have O_CLOEXEC
// flag set, so are not inherited by the executed process. ExtraFiles
// argument supported by the Go exec package applies only to Files
// open using the Go API, which always passes O_CLOEXEC flag when
// opening files and then reverts the flag for files on the ExtraFiles
// list. File descriptors passed from the parent process, or file
// descriptors that are created by direct Linux syscalls without
// O_CLOEXEC flag are by convention not closed by Go when new process
// is executed.
func AllFdsCloseOnExec() error {
	return unix.CloseRange(3, math.MaxInt32, unix.CLOSE_RANGE_CLOEXEC)
}

func writeCoverage() error {
	coverdir := os.Getenv("GOCOVERDIR")
	if coverdir != "" {
		coverage.WriteMetaDir(coverdir)
		coverage.WriteCountersDir(coverdir)
	}
	return nil
}
