package main

import (
	"errors"
	"flag"
	"fmt"
	"golang.org/x/sys/unix" // Needed only for CAP_* consts
	"io/fs"
	"kernel.org/pub/linux/libs/security/libcap/cap"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/wrr/dirjail/internal/config"
	"github.com/wrr/dirjail/internal/netns"
)

type JailPaths struct {
	cwd        string
	base       string
	fsRoot     string
	hostHome   string
	home       string
	etc        string
	tmpSrc     string
	tmpDst     string
	emptyDir   string
	emptyFile  string
	resolvConf string
}

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

func ensureDirWithNoPerms(path string) error {
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", path)
		}
		if info.Mode().Perm() == 0000 {
			// Directory exists and has correct permissions.
			return nil
		}
		// Directory doesn't have correct permissions, remove
		// and recreate it.
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Mkdir(path, 0000)
}

func ensureEmptyFile(path string) error {
	if info, err := os.Stat(path); err == nil {
		// File exists.
		if info.Mode().Perm() == 0000 && info.Size() == 0 {
			// File already has correct permissions and is empty.
			return nil
		}
		// File is not empty or doesn't have correct permissions, remove
		// and recreate it.
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0000)
	if err != nil {
		return err
	}
	return file.Close()
}

func homeDir() string {
	currentUser, err := user.Current()
	if err != nil {
		dief("failed to get current user: %v", err)
	}
	return currentUser.HomeDir
}

func tmpDirName(cwd string) string {
	dname := strings.ReplaceAll(cwd, "/", "-")
	// Keep only a-z, A-Z, 0-9, and - characters
	reg := regexp.MustCompile("[^a-zA-Z0-9-]")
	return "dirjail" + reg.ReplaceAllString(dname, "") + "-"
}

func initTmpSubDir(paths *JailPaths) string {
	dirNameFile := filepath.Join(paths.base, ".tmp")

	// if dirNameFile exists, read its content
	if data, err := os.ReadFile(dirNameFile); err == nil {
		// No error.
		dirName := strings.TrimSpace(string(data))

		if dirName != "" {
			// Check if directory exists, is owned by current user, and has 700 permissions
			tmpSubDir := filepath.Join(os.TempDir(), dirName)

			if stat, err := os.Stat(tmpSubDir); err == nil && stat.IsDir() {
				if sysStats, ok := stat.Sys().(*syscall.Stat_t); ok {
					currentUID := os.Getuid()
					if int(sysStats.Uid) == currentUID && stat.Mode().Perm() == 0700 {
						return tmpSubDir
					}
				}
			}
		}
	}

	// New tmp sub directory is needed.
	tmpSubDir, err := os.MkdirTemp("", tmpDirName(paths.cwd))
	if err != nil {
		dief("failed to create temporary directory: %v", err)
	}
	dirName := filepath.Base(tmpSubDir)

	// Write the directory name to the file, so the dir can be re-used by
	// other dirjails with the same id
	if err := os.WriteFile(dirNameFile, []byte(dirName), 0600); err != nil {
		dief("failed to write to %v: %v", dirNameFile, err)
	}
	return tmpSubDir
}

func writeResolvConf(path string) error {
	content := "nameserver 10.0.2.3"
	return os.WriteFile(path, []byte(content), 0600)
}

func initFS() JailPaths {
	cwd, err := os.Getwd()
	if err != nil {
		dief("get current directory failed: %v", err)
	}

	base := filepath.Join(cwd, ".dirjail")
	fsRoot := filepath.Join(base, "root")
	etc := filepath.Join(base, "etc")
	paths := JailPaths{
		cwd:        cwd,
		base:       base,
		fsRoot:     fsRoot,
		hostHome:   homeDir(),
		home:       filepath.Join(base, "home"),
		etc:        etc,
		tmpDst:     filepath.Join(fsRoot, os.TempDir()),
		emptyDir:   filepath.Join(base, "empty"),
		emptyFile:  filepath.Join(base, "empty_file"),
		resolvConf: filepath.Join(etc, "resolv.conf"),
	}
	// Create necessary directories
	if err := os.MkdirAll(paths.home, 0700); err != nil {
		dief("failed to create directory %s: %v", paths.home, err)
	}

	if err := os.MkdirAll(paths.etc, 0700); err != nil {
		dief("failed to create directory %s: %v", paths.home, err)
	}

	if err := ensureDirWithNoPerms(paths.emptyDir); err != nil {
		die(err)
	}
	if err := ensureEmptyFile(paths.emptyFile); err != nil {
		die(err)
	}
	if err := writeResolvConf(paths.resolvConf); err != nil {
		dief("failed to create resolv.conf file: %v", err)
	}

	paths.tmpSrc = initTmpSubDir(&paths)

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

func hideProcFiles(procAccessible []string, paths *JailPaths) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		dief("Failed to read /proc: %v", err)
	}

	procAccessible = append(procAccessible, "uptime", "loadavg", "meminfo", "stat", "sys")

	for _, entry := range entries {
		name := entry.Name()
		fullPath := filepath.Join("/proc", name)

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
			mountDir(paths.emptyDir, fullPath, syscall.MS_BIND|syscall.MS_RDONLY)
		} else {
			mountFile(paths.emptyFile, fullPath, syscall.MS_BIND|syscall.MS_RDONLY)
		}
	}
}

func childProcessEntry(progWithArgs []string) {
	paths := initFS()

	configPath := filepath.Join(paths.hostHome, ".dirjail")
	cfg, err := config.Read(configPath)
	if err != nil {
		die(err)
	}

	syscall.Chdir("/")

	mountEntries(paths.hostHome, paths.home, cfg.HomeVisible, true)
	mountEntries(paths.hostHome, paths.home, cfg.HomeWriteable, false)

	mountDir("/", paths.fsRoot, syscall.MS_BIND|syscall.MS_REC|syscall.MS_RDONLY)

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
	opts := "lowerdir=/home/j/code/dirjail/.dirjail/etc/:/etc"
	if err := syscall.Mount("overlay", paths.fsRoot+"/etc", "overlay", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_RDONLY, opts); err != nil {
		dief("mount /etc failed: %v", err)
	}

	if err := syscall.Mount("", paths.fsRoot+"/run", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
		dief("mount /run failed: %v", err)
	}

	if err := syscall.Mount("", paths.fsRoot+"/dev", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID, "mode=755"); err != nil {
		dief("mount /dev failed: %v", err)
	}

	if err := os.Mkdir(paths.fsRoot+"/dev/shm", 0700); err != nil {
		die(err)
	}
	if err := syscall.Mount("", paths.fsRoot+"/dev/shm", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, "mode=1777"); err != nil {
		dief("mount /dev failed: %v", err)
	}

	// mkdev is not allowed in the container when running as a user,
	// even if unix.CAP_MKNOD is passed, so we map some host devices to
	// the container /dev instead.
	devices := []string{"null", "zero", "full", "random", "urandom"}
	mountEntries("/dev", paths.fsRoot+"/dev", devices, false)

	mountEntries("/dev", paths.fsRoot+"/dev/test/", devices, false)

	if err := os.Mkdir(paths.fsRoot+"/dev/pts", 0700); err != nil {
		die(err)
	}
	if err := syscall.Mount("", paths.fsRoot+"/dev/pts", "devpts", syscall.MS_NOEXEC|syscall.MS_NOSUID, ""); err != nil {
		dief("mount /dev/pts failed: %v", err)
	}

	homeDst := filepath.Join(paths.fsRoot, paths.hostHome)
	mountDir(paths.home, homeDst, syscall.MS_BIND|syscall.MS_REC)

	mountDir(paths.tmpSrc, paths.tmpDst, syscall.MS_BIND)

	// Mount current working directory
	mountDir(paths.cwd, filepath.Join(paths.fsRoot, paths.cwd), syscall.MS_BIND|syscall.MS_REC)

	if err := syscall.Chroot(paths.fsRoot); err != nil {
		dief("chroot to %s failed: %v", paths.fsRoot, err)
	}

	if err := syscall.Mount("", "/proc", "proc", 0, ""); err != nil {
		dief("mount proc failed: %v", err)
	}
	hideProcFiles(cfg.ProcReadable, &paths)

	// Hide dirjail root directory
	mountDir(paths.emptyDir, paths.base, syscall.MS_BIND|syscall.MS_RDONLY)

	// Change working directory to what it was originally
	if err := syscall.Chdir(paths.cwd); err != nil {
		dief("chdir to %s failed: %v", paths.cwd, err)
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
		childProcessEntry(os.Args[2:])
		os.Exit(0)
	}
	fmt.Println("Parent started")

	var portForwards []string
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `dirjail limits programs abilities to read and write user's files
Usage: dirjail [options] [command...]
Options:
`)
		flag.PrintDefaults()
	}

	flag.Var((*stringSlice)(&portForwards), "p", "Publish port(s) to the host. Format: [hostIP:]hostPort[:containerPort]")

	err := flag.CommandLine.Parse(os.Args[1:])
	if err != nil {
		dief("failed to parse command line: %v", err)
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
	childArgs := append([]string{"-child"}, flag.Args()...)
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
