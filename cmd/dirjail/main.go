package main

import (
	"flag"
	"fmt"
	"golang.org/x/sys/unix" // Needed only for CAP_* consts
	"kernel.org/pub/linux/libs/security/libcap/cap"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"syscall"

	"github.com/wrr/dirjail/internal/config"
)

type JailPaths struct {
	cwd       string
	base      string
	root      string
	home      string
	tmp       string
	hostHome  string
	emptyDir  string
	emptyFile string
}

func dief(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func die(err error) {
	dief("%v", err)
}

func dropAllCaps() {
	old := cap.GetProc()
	empty := cap.NewSet()
	if err := empty.SetProc(); err != nil {
		dief("failed to drop privilege: %q -> %q: %v", old, empty, err)
	}
	now := cap.GetProc()
	if cf, _ := now.Cf(empty); cf != 0 {
		dief("failed to fully drop privilege: have=%q, wanted=%q", now, empty)
	}
}

func createEmptyFile(path string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_TRUNC, 0600)
	if err != nil {
		if !os.IsExist(err) {
			dief("failed to create empty file %s: %v", path, err)
		}
	} else {
		file.Close()
	}
}

func homeDir() string {
	currentUser, err := user.Current()
	if err != nil {
		dief("failed to get current user: %v", err)
	}
	return currentUser.HomeDir
}

func initFS() JailPaths {
	cwd, err := os.Getwd()
	if err != nil {
		dief("get current directory failed: %v", err)
	}

	base := filepath.Join(cwd, ".dirjail")
	paths := JailPaths{
		cwd:       cwd,
		base:      base,
		root:      filepath.Join(base, "root"),
		home:      filepath.Join(base, "home"),
		tmp:       filepath.Join(base, "tmp"),
		hostHome:  homeDir(),
		emptyDir:  filepath.Join(base, "empty"),
		emptyFile: filepath.Join(base, "empty_file"),
	}

	// Create necessary directories
	if err := os.MkdirAll(paths.home, 0755); err != nil {
		dief("failed to create directory %s: %v", paths.home, err)
	}
	if err := os.MkdirAll(paths.emptyDir, 0755); err != nil {
		dief("failed to create directory %s: %v", paths.emptyDir, err)
	}
	createEmptyFile(paths.emptyFile)
	return paths
}

func doMount(src, dst string, mountflags uintptr) {
	fmt.Printf("Mounting %s to %s\n", src, dst)
	if err := syscall.Mount(src, dst, "", mountflags, ""); err != nil {
		dief("mount %s to %s failed: %v", src, dst, err)
	}
	// mount and remount is needed for RDONLY to work:
	// https://github.com/opencontainers/runc/blob/675292473b3ad4c131b900806077148a556d78c9/libcontainer/rootfs_linux.go#L581
	if mountflags&syscall.MS_RDONLY != 0 {
		if err := syscall.Mount(dst, dst, "", syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_BIND, ""); err != nil {
			dief("readonly re-mount of %s failed: %v", dst, err)
		}
	}
}

func mountDir(src, dst string, mountflags uintptr) {
	if err := os.MkdirAll(dst, 0700); err != nil {
		dief("failed to create directory %s: %v", dst, err)
	}
	doMount(src, dst, mountflags)
}

func mountFile(src, dst string, mountflags uintptr) {
	dstParent := filepath.Dir(dst)
	if err := os.MkdirAll(dstParent, 0755); err != nil {
		dief("failed to create directory %s: %v", dstParent, err)
	}
	// Mount destination must exist, create an empty file to be the
	// destination mount point
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		createEmptyFile(dst)
	}
	doMount(src, dst, mountflags)
}

func mountEntries(srcDir, dstDir string, entries []string, readonly bool) {
	mountflags := uintptr(syscall.MS_BIND)
	if readonly {
		mountflags |= syscall.MS_RDONLY
	}

	for _, entry := range entries {
		entryPath := filepath.Join(srcDir, entry)
		newEntryPath := filepath.Join(dstDir, entry)

		if info, err := os.Stat(entryPath); err == nil {
			if info.IsDir() {
				mountDir(entryPath, newEntryPath, mountflags)
			} else {
				mountFile(entryPath, newEntryPath, mountflags)
			}
		} else {
			fmt.Printf("Not mounting %s, no such file or directory\n", entryPath)
		}
	}
}

func childProcessEntry() {
	dirs := initFS()

	configPath := filepath.Join(dirs.hostHome, ".dirjail")
	cfg, err := config.Read(configPath)
	if err != nil {
		die(err)
	}

	syscall.Chdir("/")

	mountEntries(dirs.hostHome, dirs.home, cfg.HomeVisible, true)
	mountEntries(dirs.hostHome, dirs.home, cfg.HomeWriteable, false)

	mountDir("/", dirs.root, syscall.MS_BIND|syscall.MS_REC|syscall.MS_RDONLY)

	if err := syscall.Mount("/proc", "/proc", "proc", 0, ""); err != nil {
		dief("mount proc failed: %v", err)
	}

	homeDst := filepath.Join(dirs.root, dirs.hostHome)
	mountDir(dirs.home, homeDst, syscall.MS_BIND|syscall.MS_REC)

	if err := syscall.Chroot(dirs.root); err != nil {
		dief("chroot to %s failed: %v", dirs.root, err)
	}

	// Drop all the capabilities in the user namespace. This is done
	// just in case and can be revisited later, currently there is no
	// case which shows that dropping capabilities is required to
	// guarantee proper isolation. Even with capabilities kept,
	// processes in the namespace only have privileges that the user
	// that created the namespace had, keeping the capabilities would be
	// more or less equivalent of running as root in a Docker container
	// - a normal practice.
	dropAllCaps()

	os.Setenv("debian_chroot", "dirjail")

	// TODO: use SHELL env variable
	pname := "bash"
	cmd := exec.Command(pname)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		dief("%s failed: %v", pname, err)
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
		dief("failed to parse command line: %v", err)
	}

	// /proc/self/exe would be better, because it handles the case of
	// the current binary being removed
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
		AmbientCaps: []uintptr{unix.CAP_SYS_ADMIN, unix.CAP_SYS_CHROOT},

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
		GidMappingsEnableSetgroups: false,
	}
	if err := cmd.Start(); err != nil {
		dief("jailed process start failed: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			if exitError.ExitCode() != 1 {
				// If exit code is 1, child should already print an
				// error message.
				fmt.Fprintf(os.Stderr, "Error: jailed process failed\n")
			}
			// Propage exit code of the child
			os.Exit(exitError.ExitCode())
		}
		dief("jailed process failed to run: %v", err)
	}
}
