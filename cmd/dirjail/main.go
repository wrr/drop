package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"

	"golang.org/x/sys/unix" // Needed only for CAP_* consts
	"kernel.org/pub/linux/libs/security/libcap/cap"

	"github.com/wrr/dirjail/internal/config"
	"github.com/wrr/dirjail/internal/env"
	"github.com/wrr/dirjail/internal/jailfs"
	"github.com/wrr/dirjail/internal/netns"
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

	// /proc/self/exe would be better, because it handles the case of
	// the current binary being removed
	//
	// This passes all the arguments correctly also when one of them
	// (configPath) is an empty string
	childArgs := append([]string{"-child", jailId, configPath}, flag.Args()...)
	cmd := exec.Command(os.Args[0], childArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
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
		AmbientCaps: []uintptr{unix.CAP_SYS_ADMIN, unix.CAP_SYS_CHROOT, unix.CAP_DAC_OVERRIDE, unix.CAP_FOWNER},

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
	}
	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("jailed process start failed: %v", err)
	}

	// Start slirp4netns to provide network connectivity to the jailed process
	cleanup, err := netns.StartSlirp4netns(cmd.Process.Pid, parsedPortForwards)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return 1, err
	}
	defer cleanup()

	if err := cmd.Wait(); err != nil {
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
	if len(os.Args) < 4 {
		return 1, fmt.Errorf("incorrect number of arguments; -child is an internal argument and should not be passed directly")
	}
	fmt.Printf("Child started %v\n", os.Args[0])
	jailId := os.Args[2]
	configPath := os.Args[3]
	progWithArgs := os.Args[4:]

	paths, err := jailfs.NewPaths(jailId, configPath)
	if err != nil {
		return 1, err
	}
	defer paths.Clear()

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
