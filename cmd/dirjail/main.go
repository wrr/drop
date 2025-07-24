package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"syscall"
	"golang.org/x/sys/unix" 	// Needed only for CAP_SYS_ADMIN const
	"kernel.org/pub/linux/libs/security/libcap/cap"
)

type JailPaths struct {
	jailDir    string
	emptyDir   string
	emptyFile  string
	newRootDir string
	newHomeDir string
	homeDir    string
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func dropAllCaps() {
	old := cap.GetProc()
	empty := cap.NewSet()
	if err := empty.SetProc(); err != nil {
		die("failed to drop privilege: %q -> %q: %v", old, empty, err)
	}
	now := cap.GetProc()
	if cf, _ := now.Cf(empty); cf != 0 {
		die("failed to fully drop privilege: have=%q, wanted=%q", now, empty)
	}
}

func createEmptyFile(path string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_TRUNC, 0600)
	if err != nil {
		if !os.IsExist(err) {
			die("failed to create empty file %s: %v", path, err)
		}
	} else {
		file.Close()
	}
}

func homeDir() string {
	currentUser, err := user.Current()
	if err != nil {
		die("failed to get current user: %v", err)
	}
	return currentUser.HomeDir
}

func initFS() JailPaths {
	cwd, err := os.Getwd()
	if err != nil {
		die("get current directory failed: %v", err)
	}

	jailDir := filepath.Join(cwd, ".dirjail")
	paths := JailPaths{
		jailDir:    jailDir,
		emptyDir:   filepath.Join(jailDir, "empty"),
		emptyFile:  filepath.Join(jailDir, "empty_file"),
		newRootDir: filepath.Join(jailDir, "root"),
		newHomeDir: filepath.Join(jailDir, "home"),
		homeDir:    homeDir(),
	}

	if err := os.MkdirAll(paths.emptyDir, 0755); err != nil {
		die("failed to create directory %s: %v", paths.emptyDir, err)
	}
	createEmptyFile(paths.emptyFile)
	return paths
}

func doMount(src, dst string, mountflags uintptr) {
	fmt.Printf("Mounting %s to %s\n", src, dst)
	if err := syscall.Mount(src, dst, "", mountflags, ""); err != nil {
		die("mount %s to %s failed: %v", src, dst, err)
	}
	// mount and remount is needed for RDONLY to work:
	// https://github.com/opencontainers/runc/blob/675292473b3ad4c131b900806077148a556d78c9/libcontainer/rootfs_linux.go#L581
	if mountflags&syscall.MS_RDONLY != 0 {
		if err := syscall.Mount(dst, dst, "", syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_BIND, ""); err != nil {
			die("readonly re-mount of %s failed: %v", dst, err)
		}
	}
}

func mountDir(src, dst string, mountflags uintptr) {
	if err := os.MkdirAll(dst, 0700); err != nil {
		die("failed to create directory %s: %v", dst, err)
	}
	doMount(src, dst, mountflags)
}

func childProcessEntry() {
	dirs := initFS()

	syscall.Chdir("/")

	mountDir("/", dirs.newRootDir, syscall.MS_BIND|syscall.MS_REC|syscall.MS_RDONLY)

	if err := syscall.Mount("/proc", "/proc", "proc", 0, ""); err != nil {
		die("mount proc failed: %v", err)
	}

	// Drop CAP_SYS_ADMIN in a user namespace that was needed to execute
	// mounts. This is done just in case and can be revisited later,
	// currently there is no case which shows that dropping
	// CAP_SYS_ADMIN is required to guarantee proper isolation. This is
	// admin in a user namespace, so has only as much privileges as the
	// user that created the namespace, keeping the admin would be more
	// or less equivalent of running as root in a Docker container - a
	// normal practice.
	dropAllCaps()

	// TODO: use SHELL env variable
	pname := "bash"
	cmd := exec.Command(pname)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		die("%s failed: %v", pname, err)
	}
	// Ignore errors (bash exits with an error if last executed command
	// exited with an error)
	cmd.Wait()
}

func main() {

	if len(os.Args) > 1 && os.Args[1] == "-child" {
		fmt.Printf("Child started %v\n", os.Args[0])
		childProcessEntry()
		os.Exit(0)
	}
	fmt.Println("Parent started")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "dirjail limits programs abilities to read and write user's files\nOptions:\n")
		flag.PrintDefaults()
	}

	err := flag.CommandLine.Parse(os.Args[1:])
	if err != nil {
		die("failed to parse command line: %v", err)
	}

	// /proc/self/exe would be better, because it handles the case of the current binary being removed
	cmd := exec.Command(os.Args[0], "-child")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: (syscall.CLONE_NEWNS |
			syscall.CLONE_NEWIPC |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWUSER),
		// Code running in a user namespace just after clone() and before
		// execve() system calls has all capabilities in this namespace.
		// This allows such code to setup the namespace with all required
		// mount() calls.
		// Unfortunately, GO doesn't allow to execute any user provided
		// code between clone() and execve()
		// (https://github.com/golang/go/issues/12125)
		//
		// The first process executed within the namespace with execve no
		// longer has all capabilities in the namespace (unless it has
		// root uid, but we don't want this). We need to pass
		// CAP_SYS_ADMIN capability to this process, so the process can
		// call mount(), and then we drop CAP_SYS_ADMIN.  For the detailed
		// description of how capabilities are passed and dropped in the
		// user namespace see: man 7 user_namespaces
		AmbientCaps: []uintptr{unix.CAP_SYS_ADMIN},

		// Keep using current user uid an gid in the jail (so the user is
		// recognized as the same user)
		UidMappings: []syscall.SysProcIDMap{
			{
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
		GidMappingsEnableSetgroups: false,
	}
	if err := cmd.Start(); err != nil {
		die("jailed process start failed: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		die("jailed process exited with error: %v", err)
	}
}
