package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"kernel.org/pub/linux/libs/security/libcap/cap"

	"github.com/wrr/dirjail/internal/config"
	"github.com/wrr/dirjail/internal/env"
	"github.com/wrr/dirjail/internal/jailfs"
	"github.com/wrr/dirjail/internal/netns"
)

// stringSlice implements flag.Value interface for repeated string flags
type stringSlice []string

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

func parentProcessEntry() (int, error) {
	var portForwards []string
	var jailId string
	var configPath string
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `dirjail limits programs abilities to read and write user's files
Usage: dirjail [options] [command...]
Options:
`)
		flag.PrintDefaults()
	}

	flag.Var((*stringSlice)(&portForwards), "p", "Publish port(s) to the host. Format: [hostIP:]hostPort[:containerPort]")
	flag.StringVar(&jailId, "i", "", "Jail ID")
	flag.StringVar(&configPath, "c", "", "Path to config file")

	err := flag.CommandLine.Parse(os.Args[1:])
	if err != nil {
		return 1, fmt.Errorf("failed to parse command line: %v", err)
	}

	if jailId == "" {
		jailId, err = defaultJailId()
		if err != nil {
			return 1, err
		}
	} else {
		if !isJailIdValid(jailId) {
			return 1, fmt.Errorf("invalid character in jail ID")
		}
	}

	var parsedPortForwards []*config.PortForward
	for _, pf := range portForwards {
		parsed, err := config.ParsePortForward(pf)
		if err != nil {
			return 1, fmt.Errorf("invalid port forwarding specification '%s': %v", pf, err)
		}
		parsedPortForwards = append(parsedPortForwards, parsed)
	}

	runDir, err := jailfs.NewRunDir(jailId)
	if err != nil {
		return 1, fmt.Errorf("failed to create run dir: %v", err)
	}
	defer jailfs.CleanRunDir(runDir)

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
	childArgs := append([]string{"-child", jailId, configPath, runDir}, flag.Args()...)
	cmd := exec.Command(os.Args[0], childArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{childEnd}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: (syscall.CLONE_NEWNS |
			syscall.CLONE_NEWIPC |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWUSER |
			syscall.CLONE_NEWNET),
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

		// Keep using current user uid an gid in the jail (so the user is
		// recognized as the same user)
		UidMappings: []syscall.SysProcIDMap{
			{
				//ContainerID: os.Getuid(),
				ContainerID: os.Getuid(),
				HostID:      os.Getuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: os.Getgid(),
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

	// Start slirp4netns to provide network connectivity to the jailed process
	cleanup, err := netns.StartSlirp4netns(cmd.Process.Pid, parsedPortForwards, runDir)
	if err != nil {
		return 1, err
	}
	defer cleanup()

	// Signal child process that setup is finished. This needs to be
	// done when slirp4netns is running, because only then the child
	// can run netns.SetupFirewall
	if err := signalParentReady(parentEnd); err != nil {
		return 1, err
	}

	err = cmd.Wait()
	cmd = nil
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode := exitError.ExitCode()
			var err error
			if exitCode != 1 {
				// If exit code is 1, child should already print an
				// error message.
				err = fmt.Errorf("jailed process failed")
			}
			// Propage exit code of the child
			return exitCode, err
		}
		return 1, fmt.Errorf("jailed process failed to run: %v", err)
	}
	return 0, nil
}

func childProcessEntry() (int, error) {
	if len(os.Args) < 5 {
		return 1, fmt.Errorf("incorrect number of arguments; -child is an internal argument and should not be passed directly")
	}
	jailId := os.Args[2]
	configPath := os.Args[3]
	runDir := os.Args[4]
	progWithArgs := os.Args[5:]

	// Wait for parent to signal that it has finished setup.
	// The read end of the pipe is inherited as file descriptor 3
	readEnd := os.NewFile(3, "parent-ready-pipe")
	if readEnd == nil {
		return 1, fmt.Errorf("failed to get pipe to parent")
	}
	if err := waitParentReady(readEnd); err != nil {
		return 1, err
	}

	// Can be done only after parent setup is done (slirp4netns is
	// started).
	if err := netns.SetupFirewall(); err != nil {
		return 1, err
	}

	paths, err := jailfs.NewPaths(jailId, configPath, runDir)
	if err != nil {
		return 1, err
	}

	cfg, err := config.Read(paths.Config)
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
	// CAP_SYS_ADMIN would allow the user to umount dirjail mounts and
	// access the original directories (home dir, proc etc.)
	if err := dropAllCaps(); err != nil {
		return 1, err
	}

	var cmd *exec.Cmd

	if len(progWithArgs) == 0 {
		// TODO: use SHELL env variable
		progWithArgs = []string{"bash"}
	}

	pname := progWithArgs[0]
	cmd = exec.Command(pname, progWithArgs[1:]...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Filter environment variables and always include debian_chroot
	filteredEnv := env.Filter(os.Environ(), cfg.EnvExpose)
	cmd.Env = append([]string{"debian_chroot=dirjail"}, filteredEnv...)

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("%s failed: %v", progWithArgs[0], err)
	}
	// Ignore errors (bash exits with an error if last executed command
	// exited with an error)
	cmd.Wait()
	return 0, nil
}

var jailIdChars = `a-zA-Z0-9-_\.`

func defaultJailId() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory failed: %v", err)
	}
	dname := strings.ReplaceAll(cwd, "/", "-")
	// remove leading - not to start directory name with -
	if len(dname) <= 1 {
		return "root", nil
	}
	dname = dname[1:]
	// Keep only allowed jail ID characters
	reg := regexp.MustCompile(`[^` + jailIdChars + `]`)
	return reg.ReplaceAllString(dname, "_"), nil
}

func isJailIdValid(jailId string) bool {
	reg := regexp.MustCompile(`^[` + jailIdChars + `]+$`)
	// Do not allow - at the start, because directory created for this
	// jail will then be tricky to handle with standard shell tools
	// (directory name interpreted as a command flag).
	return jailId[0] != '-' && reg.MatchString(jailId)
}

func FD_SET(fd int, p *syscall.FdSet) {
	p.Bits[fd/64] |= 1 << (uint(fd) % 64)
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
