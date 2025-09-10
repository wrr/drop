package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix" // Needed only for CAP_* consts
	"kernel.org/pub/linux/libs/security/libcap/cap"

	"github.com/wrr/dirjail/internal/config"
	"github.com/wrr/dirjail/internal/jailfs"
	"github.com/wrr/dirjail/internal/netns"
)

func errorf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}

func dief(format string, args ...interface{}) {
	errorf(format, args...)
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

func createEmptyFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0000)
	if err != nil {
		return fmt.Errorf("failed creating %s: %w", path, err)
	}
	return file.Close()
}

var jailIdChars = `a-zA-Z0-9-_\.`

func defaultJailId() string {
	cwd, err := os.Getwd()
	if err != nil {
		dief("get current directory failed: %v", err)
	}
	dname := strings.ReplaceAll(cwd, "/", "-")
	// remove leading - not to start directory name with -
	if len(dname) <= 1 {
		return "root"
	}
	dname = dname[1:]
	// Keep only allowed jail ID characters
	reg := regexp.MustCompile(`[^` + jailIdChars + `]`)
	return reg.ReplaceAllString(dname, "_")
}

func isJailIdValid(jailId string) bool {
	reg := regexp.MustCompile(`^[` + jailIdChars + `]+$`)
	// Do not allow - at the start, because directory created for this
	// jail will then be tricky to handle with standard shell tools
	// (directory name interpreted as a command flag).
	return jailId[0] != '-' && reg.MatchString(jailId)
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
	if err := os.MkdirAll(dstParent, 0750); err != nil {
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

var digitsRegex = regexp.MustCompile(`^\d+$`)

func allDigits(s string) bool {
	return digitsRegex.MatchString(s)
}

func hideProcFiles(procAccessible []string, paths *jailfs.Paths) {
	procRoot := paths.FsRoot + "/proc"
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		dief("Failed to read %v: %v", procRoot, err)
	}

	procAccessible = append(procAccessible, "uptime", "loadavg", "meminfo", "stat", "sys")

	for _, entry := range entries {
		name := entry.Name()
		fullPath := filepath.Join(procRoot, name)

		// Proc entries with all digits are connected to processes with
		// the same id. Proc contains only processes started in the jail,
		// so all these entries are accessible.
		accessible := allDigits(name) || slices.Contains(procAccessible, name)
		if accessible {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Removed after ReadDir returned
				continue
			}
			dief("failed to retrieve file info for %s %v", fullPath, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			// skip symlinks such as /proc/self
		} else if info.IsDir() {
			mountDir(paths.EmptyDir, fullPath, syscall.MS_BIND|syscall.MS_RDONLY)
		} else {
			mountFile(paths.EmptyFile, fullPath, syscall.MS_BIND|syscall.MS_RDONLY)
		}
	}
}

func childProcessEntry(jailId string, progWithArgs []string) {
	paths, err := jailfs.NewPaths(jailId)
	if err != nil {
		die(err)
	}

	cfg, err := config.Read(paths.Config)
	if err != nil {
		die(err)
	}

	syscall.Chdir("/")

	mountEntries(paths.HostHome, paths.Home, cfg.HomeVisible, true)
	mountEntries(paths.HostHome, paths.Home, cfg.HomeWriteable, false)

	mountDir("/", paths.FsRoot, syscall.MS_BIND|syscall.MS_REC|syscall.MS_RDONLY)

	// For DNS to work in the container /etc/resolv.conf needs to be
	// overwritten. We use overlayfs for this instead of bind mounting
	// /etc/resolv.conf. On Ubuntu /etc/resolv.conf is a symlink to
	// ../run/systemd/resolve/stub-resolv.conf. It is not possible for a
	// bind mount to replace a symlink, so our resolv.conf would still
	// need to be at ../run/systemd/resolve/stub-resolv.conf. Having
	// read-only overlayfs with our /etc/resolv.conf in a top level hides
	// the symlink, so is more elegant and also allows to easily replace more
	// config files as needed.
	//
	// Readonly overlayfs does not require upperdir= and workdir= params.
	opts := fmt.Sprintf("lowerdir=%s:/etc", paths.Etc)
	if err := syscall.Mount("overlay", paths.FsRoot+"/etc", "overlay", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_RDONLY, opts); err != nil {
		dief("mount /etc failed: %v", err)
	}

	if err := syscall.Mount("", paths.FsRoot+"/run", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
		dief("mount /run failed: %v", err)
	}

	if err := syscall.Mount("", paths.FsRoot+"/dev", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID, "mode=755"); err != nil {
		dief("mount /dev failed: %v", err)
	}

	if err := os.Mkdir(paths.FsRoot+"/dev/shm", 0700); err != nil {
		die(err)
	}
	if err := syscall.Mount("", paths.FsRoot+"/dev/shm", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, "mode=1777"); err != nil {
		dief("mount /dev failed: %v", err)
	}

	// mkdev is not allowed in the container when running as a user,
	// even if unix.CAP_MKNOD is passed, so we map some host devices to
	// the container /dev instead.
	devices := []string{"null", "zero", "full", "random", "urandom"}
	mountEntries("/dev", paths.FsRoot+"/dev", devices, false)

	mountEntries("/dev", paths.FsRoot+"/dev/test/", devices, false)

	if err := os.Mkdir(paths.FsRoot+"/dev/pts", 0700); err != nil {
		die(err)
	}
	if err := syscall.Mount("", paths.FsRoot+"/dev/pts", "devpts", syscall.MS_NOEXEC|syscall.MS_NOSUID, ""); err != nil {
		dief("mount /dev/pts failed: %v", err)
	}

	homeDst := filepath.Join(paths.FsRoot, paths.HostHome)
	mountDir(paths.Home, homeDst, syscall.MS_BIND|syscall.MS_REC)

	mountDir(paths.TmpSrc, paths.TmpDst, syscall.MS_BIND)

	// Mount current working directory
	mountDir(paths.Cwd, filepath.Join(paths.FsRoot, paths.Cwd), syscall.MS_BIND|syscall.MS_REC)

	if err := syscall.Mount("", paths.FsRoot+"/proc", "proc", 0, ""); err != nil {
		dief("mount proc failed: %v", err)
	}
	hideProcFiles(cfg.ProcReadable, paths)

	if err := syscall.Chroot(paths.FsRoot); err != nil {
		dief("chroot to %s failed: %v", paths.FsRoot, err)
	}

	// Change working directory to what it was originally
	if err := syscall.Chdir(paths.Cwd); err != nil {
		dief("chdir to %s failed: %v", paths.Cwd, err)
	}

	// Drop all the capabilities in the user namespace.
	//
	// CAP_SYS_ADMIN would allow the user to umount dirjail mounts and
	// access the original directories (home dir, proc etc.)
	dropAllCaps()

	os.Setenv("debian_chroot", "dirjail")

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

	if err := cmd.Start(); err != nil {
		dief("%s failed: %v", progWithArgs[0], err)
	}
	// Ignore errors (bash exits with an error if last executed command
	// exited with an error)
	cmd.Wait()
}

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
	if len(os.Args) > 1 && os.Args[1] == "-child" {
		fmt.Printf("Child started %v\n", os.Args[0])
		jailId := os.Args[2]
		cmdAndArgs := os.Args[3:]
		childProcessEntry(jailId, cmdAndArgs)
		os.Exit(0)
	}
	fmt.Println("Parent started")

	var portForwards []string
	var jailId string
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `dirjail limits programs abilities to read and write user's files
Usage: dirjail [options] [command...]
Options:
`)
		flag.PrintDefaults()
	}

	flag.Var((*stringSlice)(&portForwards), "p", "Publish port(s) to the host. Format: [hostIP:]hostPort[:containerPort]")
	flag.StringVar(&jailId, "i", "", "Jail ID")

	err := flag.CommandLine.Parse(os.Args[1:])
	if err != nil {
		dief("failed to parse command line: %v", err)
	}

	if jailId == "" {
		jailId = defaultJailId()
	} else {
		if !isJailIdValid(jailId) {
			dief("invalid character in jail ID")
		}
	}

	var parsedPortForwards []*config.PortForward
	for _, pf := range portForwards {
		parsed, err := config.ParsePortForward(pf)
		if err != nil {
			dief("invalid port forwarding specification '%s': %v", pf, err)
		}
		parsedPortForwards = append(parsedPortForwards, parsed)
	}

	// /proc/self/exe would be better, because it handles the case of
	// the current binary being removed
	childArgs := append([]string{"-child", jailId}, flag.Args()...)
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
		dief("jailed process start failed: %v", err)
	}

	// Start slirp4netns to provide network connectivity to the jailed process
	cleanup, err := netns.StartSlirp4netns(cmd.Process.Pid, parsedPortForwards)
	if err != nil {
		errorf("%v", err)
		cmd.Process.Kill()
	}
	defer cleanup()

	if err := cmd.Wait(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			if exitError.ExitCode() != 1 {
				// If exit code is 1, child should already print an
				// error message.
				errorf("jailed process failed")
			}
			// Propage exit code of the child
			os.Exit(exitError.ExitCode())
		}
		dief("jailed process failed to run: %v", err)
	}
}
