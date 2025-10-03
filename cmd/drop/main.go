package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"kernel.org/pub/linux/libs/security/libcap/cap"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/env"
	"github.com/wrr/drop/internal/jailfs"
	"github.com/wrr/drop/internal/netns"
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

func newPathsAndConfig(envId, homeDir, configPath, runDir string) (*jailfs.Paths, *config.Config, error) {
	paths, err := jailfs.NewPaths(envId, homeDir, configPath, runDir)
	if err != nil {
		return nil, nil, err
	}

	cfg, err := config.Read(paths.Config)
	if err != nil {
		return nil, nil, err
	}

	return paths, cfg, nil
}

func parentProcessEntry() (int, error) {
	var tcpPortsToHost []string
	var tcpPortsFromHost []string
	var udpPortsToHost []string
	var udpPortsFromHost []string
	var envId string
	var configPath string
	var networkMode string
	var beRoot bool
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `drop limits programs abilities to read and write user's files
Usage: drop [options] [command...]
Options:
`)
		flag.PrintDefaults()
	}

	flag.Var((*stringSlice)(&tcpPortsToHost), "t", "Publish TCP port(s) to the host. Format: [hostIP/]hostPort[:sandboxPort]")
	flag.Var((*stringSlice)(&tcpPortsFromHost), "T", "Publish TCP port(s) from the host. Format: [hostIP/]hostPort[:sandboxPort]")
	flag.Var((*stringSlice)(&udpPortsToHost), "u", "Publish UDP port(s) to the host. Format: [hostIP/]hostPort[:sandboxPort]")
	flag.Var((*stringSlice)(&udpPortsFromHost), "U", "Publish UDP port(s) from the host. Format: [hostIP/]hostPort[:sandboxPort]")
	flag.StringVar(&envId, "e", "", "Environment ID")
	flag.StringVar(&configPath, "c", "", "Path to config file")
	flag.StringVar(&networkMode, "n", "isolated", "Network mode: off, isolated, or unjailed")
	flag.BoolVar(&beRoot, "r", false, "Be root (uid 0) in the jail. Useful for running installation scripts that\n"+
		"require to be run as root. This option doesn't grant any additional privileges to the jailed\n"+
		"processes. For convenience, the home dir of a root user is not set to /root, but\n"+
		"kept as the original home dir.")

	err := flag.CommandLine.Parse(os.Args[1:])
	if err != nil {
		return 1, fmt.Errorf("failed to parse command line: %v", err)
	}

	if networkMode != "off" && networkMode != "isolated" && networkMode != "unjailed" {
		return 1, fmt.Errorf("invalid network mode '%s': must be 'off', 'isolated', or 'unjailed'", networkMode)
	}

	// Obtain home dir in the parent, because with -r option child we
	// be run as root, but we don't want to use /root as the home dir.
	homeDir, err := currentUserHomeDir()
	if err != nil {
		return 1, err
	}

	if envId == "" {
		envId, err = jailfs.CwdToEnvId()
		if err != nil {
			return 1, err
		}
	} else {
		if !jailfs.IsEnvIdValid(envId) {
			return 1, fmt.Errorf("invalid character in env ID")
		}
	}

	runDir, err := jailfs.NewRunDir(homeDir, envId)
	if err != nil {
		return 1, fmt.Errorf("failed to create run dir: %v", err)
	}
	defer jailfs.CleanRunDir(runDir)

	_, cfg, err := newPathsAndConfig(envId, homeDir, configPath, runDir)
	if err != nil {
		return 1, err
	}

	if (len(tcpPortsToHost) > 0 ||
		len(tcpPortsFromHost) > 0 ||
		len(udpPortsToHost) > 0 ||
		len(udpPortsFromHost) > 0) &&
		networkMode != "isolated" {
		return 1, fmt.Errorf("port forwarding is only supported with isolated network mode (-n isolated)")
	}
	// Command line flags take priority over the config file.
	if len(tcpPortsToHost) > 0 {
		cfg.Net.TCPPortsToHost = tcpPortsToHost
	}
	if len(tcpPortsFromHost) > 0 {
		cfg.Net.TCPPortsFromHost = tcpPortsFromHost
	}
	if len(udpPortsToHost) > 0 {
		cfg.Net.UDPPortsToHost = udpPortsToHost
	}
	if len(udpPortsFromHost) > 0 {
		cfg.Net.UDPPortsFromHost = udpPortsFromHost
	}

	// Pipe for synchronizing setup with the child process.
	childEnd, parentEnd, err := os.Pipe()
	if err != nil {
		return 1, fmt.Errorf("failed to create pipe: %v", err)
	}

	// /proc/self/exe would be better, because it handles the case of
	// the current binary being removed
	//
	// This passes all the arguments correctly also when one of them
	// (configPath) is an empty string
	childArgs := append([]string{"-child", envId, configPath, runDir, networkMode, homeDir}, flag.Args()...)
	cmd := exec.Command(os.Args[0], childArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{childEnd}

	cloneFlags := uintptr(syscall.CLONE_NEWNS |
		syscall.CLONE_NEWIPC |
		syscall.CLONE_NEWPID |
		syscall.CLONE_NEWUSER)
	if networkMode != "unjailed" {
		cloneFlags |= syscall.CLONE_NEWNET
	}
	var containerUID, containerGID int
	if beRoot {
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
		// and chroot() calls.
		// Unfortunately, GO doesn't allow to execute any user provided
		// code between clone() and execve()
		// (https://github.com/golang/go/issues/12125)
		//
		// The first process executed within the namespace with execve no
		// longer has any capabilities in the namespace (unless it has
		// root uid, but we don't want this). We need to pass required
		// capabilities to this process, so the process can call mount()
		// and chroot(), and then we drop these capabilities. For the
		// detailed description of how capabilities are propagated
		// in the user namespace see: man 7 user_namespaces
		//
		// CAP_DAC_OVERRIDE and CAP_FOWNER are needed to mount overlayfs
		//
		// CAP_NET_ADMIN is needed to setup firewall with iptables
		AmbientCaps: []uintptr{
			unix.CAP_SYS_ADMIN,
			unix.CAP_SYS_CHROOT,
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
	if networkMode == "isolated" {
		cleanup, err := netns.StartPasta(cmd.Process.Pid, cfg.Net, runDir)
		if err != nil {
			return 1, err
		}
		defer cleanup()
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
	if len(os.Args) < 7 {
		return 1, fmt.Errorf("incorrect number of arguments; -child is an internal argument and should not be passed directly")
	}
	envId := os.Args[2]
	configPath := os.Args[3]
	runDir := os.Args[4]
	// networkMode := os.Args[5]
	homeDir := os.Args[6]
	progWithArgs := os.Args[7:]

	// Wait for parent to signal that it has finished setup.
	// The read end of the pipe is inherited as file descriptor 3
	readEnd := os.NewFile(3, "parent-ready-pipe")
	if readEnd == nil {
		return 1, fmt.Errorf("failed to get pipe to parent")
	}
	if err := waitParentReady(readEnd); err != nil {
		return 1, err
	}

	paths, cfg, err := newPathsAndConfig(envId, homeDir, configPath, runDir)
	if err != nil {
		return 1, err
	}

	if err := jailfs.WriteEtcFiles(paths); err != nil {
		return 1, fmt.Errorf("failed to write /etc files: %v", err)
	}

	if err := syscall.Chdir("/"); err != nil {
		return 1, fmt.Errorf("chdir to / failed: %v", err)
	}

	if err := jailfs.ArrangeFilesystem(paths, cfg); err != nil {
		return 1, err
	}

	if err := syscall.Chroot(paths.FsRoot); err != nil {
		return 1, fmt.Errorf("chroot to %s failed: %v", paths.FsRoot, err)
	}

	// Change working directory to what it was originally
	if err := syscall.Chdir(paths.Cwd); err != nil {
		return 1, fmt.Errorf("chdir to %s failed: %v", paths.Cwd, err)
	}

	// Drop all the capabilities in the user namespace.
	//
	// CAP_SYS_ADMIN would allow the user to umount drop mounts and
	// access the original directories (home dir, proc etc.)
	if err := dropAllCaps(); err != nil {
		return 1, err
	}

	if len(progWithArgs) == 0 {
		// TODO: use SHELL env variable
		progWithArgs = []string{"bash"}
	}

	// Filter environment variables and always include debian_chroot
	filteredEnv := env.Filter(os.Environ(), cfg.EnvExpose)
	envVars := append([]string{"debian_chroot=drop"}, filteredEnv...)

	prog, err := exec.LookPath(progWithArgs[0]) // Searches PATH
	if err != nil {
		return 1, fmt.Errorf("command not found: %v", err)
	}

	if err := AllFdsCloseOnExec(); err != nil {
		return 1, fmt.Errorf("failed to set open file descriptors to close: %v", err)
	}
	// Replace the current process
	if err := syscall.Exec(prog, progWithArgs, envVars); err != nil {
		return 1, fmt.Errorf("exec %s failed: %v", progWithArgs[0], err)
	}

	// Should never be reached
	return 3, fmt.Errorf("exec failed")
}

func FD_SET(fd int, p *syscall.FdSet) {
	p.Bits[fd/64] |= 1 << (uint(fd) % 64)
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

// discardChildTermInjection checks if any input is pending on
// the unjailed parent standard input and discards it.
//
// This is to prevent the terminating jailed process from injecting
// terminal input to the unjailed parent, thus executing code outside
// of the jail.
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

		var readfds syscall.FdSet
		const stdin int = 0
		FD_SET(stdin, &readfds)

		n, err := syscall.Select(stdin+1, &readfds, nil, nil, &syscall.Timeval{Sec: 0, Usec: 0})
		if err != nil {
			fmt.Printf("Failed to discard jailed process stdin leftowers: %v", err)
			return
		}
		if n == 0 {
			// Nothing available
			return
		}
		buf := make([]byte, 128)
		_, err = syscall.Read(stdin, buf)
		if err != nil {
			fmt.Printf("Failed to discard jailed process stdin leftowers: %v", err)
			return
		}
		fmt.Fprintf(os.Stderr, "Discarding %s\n", string(buf))
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
