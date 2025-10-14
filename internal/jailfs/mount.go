package jailfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/osutil"
)

// alwaysBlocked contains paths that are always blocked regardless of
// user configuration. We block the same paths that runc default
// configuration blocks.
//
// In addition runc default configuration sets
// the following paths to be read-only:
// "/proc/bus",
// "/proc/fs",
// "/proc/irq",
// "/proc/sys",
// "/proc/sysrq-trigger"
// We skip this step, because Linux already makes these paths
// read-only, perhaps this is needed in case container is started as root,
// which Drop doesn't allow.
//
// The motivation for blocking /sys/firmware is here:
// https://github.com/moby/moby/pull/26618
var alwaysBlocked = []string{
	"/proc/acpi",
	"/proc/asound",
	"/proc/kcore",
	"/proc/keys",
	"/proc/latency_stats",
	"/proc/timer_list",
	"/proc/timer_stats",
	"/proc/sched_debug",
	"/proc/scsi",
	"/sys/firmware",
}

// ArrangeFilesystem sets up the jail filesystem hierarchy.
func ArrangeFilesystem(paths *Paths, cfg *config.Config) error {
	// Change all mounts propagation to PRIVATE (default is SHARED). See man
	// mount_namespaces and man 1 unshare.
	//
	// The important effect of this is that mounts done on the host
	// while Drop instance is running are not accessible to this
	// instance (Permission denied during access), they require Drop
	// restart to become accessible. For example, if the user exposes
	// /media as read-only to Drop, starts Drop and then mounts USB
	// memory device to /media/usb, this memory device is not accessible
	// within the running Drop instance. This is desirable, because Drop
	// cannot force new mounts to be read-only, it can only set
	// read-only flag for mounts existing while it is starting, so if
	// such mounted USB was exposed by MS_SHARED mode, it would be
	// writable from within Drop.
	if err := mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("failed to remount / as private")
	}

	if err := mountRootSubDir(paths); err != nil {
		return err
	}

	if err := mountHome(paths, cfg); err != nil {
		return err
	}

	if err := mountEtc(paths); err != nil {
		return err
	}

	if err := mountRun(paths); err != nil {
		return err
	}

	if err := mountDev(paths); err != nil {
		return err
	}

	tmpDst := filepath.Join(paths.FsRoot, os.TempDir())
	if err := bindDir(paths.Tmp, tmpDst, 0); err != nil {
		return err
	}

	// Mount current working directory and it's subdirs as readable and
	// writable, but only if Cwd is not the home directory or a parent of it,
	// to avoid exposing the original home directory.
	if !isSubDirOrSame(paths.Cwd, paths.HostHome) {
		if err := bindDir(paths.Cwd, filepath.Join(paths.FsRoot, paths.Cwd), 0); err != nil {
			return err
		}
	}

	if err := mountProc(paths); err != nil {
		return err
	}

	if err := mountSys(paths, cfg); err != nil {
		return err
	}

	if err := mountVar(paths); err != nil {
		return err
	}

	// Combine always blocked paths with user-configured blocked paths
	blockedPaths := append(alwaysBlocked, cfg.Blocked...)
	if err := blockFsRootEntries(paths, blockedPaths); err != nil {
		return err
	}

	return pivotRoot(paths)
}

// mount is a simple wrapper over Mount which makes sure that target
// directory exists. It creates target dir with permissions 0700 if it
// is missing.
func mount(source string, target string, fstype string, flags uintptr, data string) (err error) {
	if err := osutil.MkdirAll(target); err != nil {
		return err
	}
	return syscall.Mount(source, target, fstype, flags, data)
}

// mountRootSubDir bind mounts all exposed sub-dirs of /.
func mountRootSubDir(paths *Paths) error {
	// The initial approach was just to mount / into paths.fsRoot and
	// then block paths configured to be not accessible. This had a nice
	// property that / automatically had identical content to the host
	// root (with some directories not accessible, but all directories
	// present). The drawback was that all the original submounts of /
	// become visible in Drop via /proc/mounts, and Linux does not allow
	// to unmount them. For example, /snap dir has a ton of application
	// specific mounts and mounting / makes all of them visible in Drop.
	//
	// If there are any submounts MS_REC is required in the
	// user namespace (mount fails without it). This is because if the
	// host hides some dir content by mounting over it, all these mounts
	// need to be still available in the user namespace.  If dropping
	// mounts by not using MS_REC option was possible, it would enable
	// exposing of the hidden content (See man mount_namespaces).
	//
	// Unfortunately, submounts will not be set to read-only, so we need
	// to iterate all the mounts and set read-only flag for them (TODO).
	flags := uintptr(syscall.MS_NOSUID | syscall.MS_REC | syscall.MS_RDONLY | syscall.MS_PRIVATE)
	dirs := []string{"/usr", "/bin", "/lib", "/lib32", "/lib64", "/sbin"}

	for _, src := range dirs {
		info, err := os.Lstat(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to stat %s: %v", src, err)
		}

		dst := filepath.Join(paths.FsRoot, src)

		if info.Mode()&os.ModeSymlink != 0 {
			// src is a symbolic link, copy it (on many distros /bin /sbin
			// /lib* are just links to sub dirs of /usr)
			target, err := os.Readlink(src)
			if err != nil {
				return fmt.Errorf("failed to read symlink %s: %v", src, err)
			}
			if err := os.Symlink(target, dst); err != nil {
				return fmt.Errorf("failed to create symlink %s -> %s: %v", dst, target, err)
			}
		} else if info.IsDir() {
			// src is a directory, bind mount it
			if err := bindDir(src, dst, flags); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("%s is neither a directory nor a symbolic link", src)
		}
	}

	return nil
}

// mountHome mounts the user's home directory in the jail filesystem.
//
// Jail hides the real user's home dir from the host. Home dirs are
// shared by jails with the same environment id.
//
// Home dirs have HomeVisible and HomeWriteable entries exposed from
// the host home directory. To expose these entries we need to create
// empty files and directories as mount points. In order not to polute
// the jail home dir with these empty files and dirs, we use
// overlayfs. Empty dirs and files are created in a disposable
// lowerdir of the overlayfs (kept in the jails's 'run' dir), which is
// removed when the jail terminates. The actual files created in the
// jailed home are written to the overlayfs upper layer.
func mountHome(paths *Paths, cfg *config.Config) error {
	homeLower := filepath.Join(paths.Run, "home-lower")
	if err := osutil.MkdirAll(homeLower); err != nil {
		return err
	}
	homeWork := filepath.Join(paths.Run, "home-work")
	if err := osutil.MkdirAll(homeWork); err != nil {
		return err
	}

	mountPoints := append(cfg.HomeVisible, cfg.HomeWriteable...)
	if isSubDir(paths.HostHome, paths.Cwd) {
		// If CWD is a subdir of home, a mountpoint for it is also needed,
		// as CWD is mounted read-write.
		cwdRelPath, err := filepath.Rel(paths.HostHome, paths.Cwd)
		if err != nil {
			return err
		}
		mountPoints = append(mountPoints, cwdRelPath)
	}

	// Create empty files and dirs to be mount point in the overlayfs
	// lower dir.
	if err := createMountPoints(paths.HostHome, homeLower, mountPoints); err != nil {
		return err
	}

	// Mount home dir as overlayfs, lowerdir holds only mount points,
	// upperdir is where the actual files are stored.
	homeDst := filepath.Join(paths.FsRoot, paths.HostHome)
	// xino=off to disable Ubuntu 24.04 dmesg warning 'overlayfs: fs on
	// '/home/...' does not support file handles, falling back to
	// xino=off'. It is very unlikely for home dir overlayfs layers to be on
	// different filesystems (~/.drop dir would need to be placed by the
	// user on a different filesystem), when layers are on the same fs
	// xino=on option has no effect.
	// https://docs.kernel.org/filesystems/overlayfs.html
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,xino=off", homeLower, paths.Home, homeWork)
	if err := mount("home", homeDst, "overlay", syscall.MS_NOSUID, opts); err != nil {
		return fmt.Errorf("mount home to %s failed: %v", homeDst, err)
	}

	if err := bindAll(paths.HostHome, homeDst, cfg.HomeVisible, true); err != nil {
		return err
	}
	if err := bindAll(paths.HostHome, homeDst, cfg.HomeWriteable, false); err != nil {
		return err
	}

	return nil
}

func createMountPoints(srcDir, dstDir string, entries []string) error {
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry)
		dst := filepath.Join(dstDir, entry)

		if info, err := os.Stat(src); err == nil {
			// No error
			if info.IsDir() {
				if err := osutil.MkdirAll(dst); err != nil {
					return err
				}
			} else {
				if err := createEmptyFile(dst); err != nil {
					return err
				}
			}
		}
		// Do nothing: if file/directory doesn't exist in the srcDir, it cannot be
		// exposed with bind mount.
	}
	return nil
}

func mountEtc(paths *Paths) error {
	flags := uintptr(syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_RDONLY)
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
	if err := mount("etc", paths.FsRoot+"/etc", "overlay", flags, opts); err != nil {
		return fmt.Errorf("mount /etc failed: %v", err)
	}
	return nil
}

func mountRun(paths *Paths) error {
	flags := uintptr(syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV)
	if err := mount("run", paths.FsRoot+"/run", "tmpfs", flags, "mode=700"); err != nil {
		return fmt.Errorf("mount /run failed: %v", err)
	}
	return nil
}

func mountDev(paths *Paths) error {
	devDst := paths.FsRoot + "/dev"
	flags := uintptr(syscall.MS_NOEXEC | syscall.MS_NOSUID)
	if err := mount("dev", devDst, "tmpfs", flags, "mode=700"); err != nil {
		return fmt.Errorf("mount /dev failed: %v", err)
	}
	if err := mount("dev-shm", devDst+"/shm", "tmpfs", flags|syscall.MS_NODEV, "mode=1700"); err != nil {
		return fmt.Errorf("mount /dev/shm failed: %v", err)
	}

	// mkdev is not allowed in the container when running as a user,
	// even if unix.CAP_MKNOD is passed, so we map some host devices to
	// the container /dev instead.
	devices := []string{"null", "zero", "full", "random", "urandom"}
	if err := bindAll("/dev", devDst, devices, false); err != nil {
		return err
	}

	opts := "mode=600,newinstance,ptmxmode=600"
	if err := mount("dev-pts", devDst+"/pts", "devpts", flags, opts); err != nil {
		return fmt.Errorf("mount /dev/pts failed: %v", err)
	}

	symlinks := map[string]string{
		"ptmx":   "pts/ptmx",
		"stdin":  "/proc/self/fd/0",
		"stdout": "/proc/self/fd/1",
		"stderr": "/proc/self/fd/2",
		"fd":     "/proc/self/fd",
		"core":   "/proc/kcore",
	}
	for name, target := range symlinks {
		if err := os.Symlink(target, devDst+"/"+name); err != nil {
			return fmt.Errorf("failed to create %s symlink: %v", name, err)
		}
	}

	return nil
}

func bindAll(srcDir, dstDir string, entries []string, readonly bool) error {
	flags := uintptr(0)
	if readonly {
		flags |= syscall.MS_RDONLY
	}

	for _, entry := range entries {
		src := filepath.Join(srcDir, entry)
		dst := filepath.Join(dstDir, entry)

		if info, err := os.Stat(src); err == nil {
			if info.IsDir() {
				if err := bindDir(src, dst, flags); err != nil {
					return err
				}
			} else {
				if err := bindFile(src, dst, flags); err != nil {
					return err
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "Not mounting %s, no such file or directory\n", src)
		}
	}
	return nil
}

func bindFile(src, dst string, mountflags uintptr) error {
	// Mount destination must exist, create an empty file to be the
	// destination mount point
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := createEmptyFile(dst); err != nil {
			return err
		}
	}
	return doBind(src, dst, mountflags)
}

func bindDir(src, dst string, mountflags uintptr) error {
	if err := osutil.MkdirAll(dst); err != nil {
		return err
	}
	return doBind(src, dst, mountflags)
}

func doBind(src, dst string, mountflags uintptr) error {
	mountflags |= syscall.MS_BIND
	if err := syscall.Mount(src, dst, "", mountflags, ""); err != nil {
		return fmt.Errorf("mount %s to %s failed: %v", src, dst, err)
	}
	// mount and remount is needed for RDONLY to work:
	// https://github.com/containerd/containerd/issues/1368
	if mountflags&syscall.MS_RDONLY != 0 {
		remountflags := uintptr(syscall.MS_REMOUNT | syscall.MS_RDONLY | syscall.MS_BIND)
		if err := syscall.Mount(dst, dst, "", remountflags, ""); err != nil {
			return fmt.Errorf("readonly re-mount of %s failed: %v", dst, err)
		}
	}
	return nil
}

// createEmptyFile creates an empty file and all its missing parent directories
func createEmptyFile(path string) error {
	parent := filepath.Dir(path)
	if err := osutil.MkdirAll(parent); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}
	return file.Close()
}

// isSubDirOrSame returns true if child is a sub directory of the parent.
func isSubDir(parent, child string) bool {
	parent = cleanDir(parent)
	child = cleanDir(child)
	return child != parent && strings.HasPrefix(child, parent)
}

// isSubDirOrSame returns true if child is a sub directory of the parent
// or if they are the same directory.
func isSubDirOrSame(parent, child string) bool {
	parent = cleanDir(parent)
	child = cleanDir(child)
	return strings.HasPrefix(child, parent)
}

func cleanDir(dir string) string {
	sep := string(filepath.Separator)
	dir = filepath.Clean(dir)
	if !strings.HasSuffix(dir, sep) {
		dir += sep
	}
	return dir
}

func mountProc(paths *Paths) error {
	if err := mount("proc", paths.FsRoot+"/proc", "proc", 0, ""); err != nil {
		return fmt.Errorf("mount proc failed: %v", err)
	}
	return nil
}

func mountSys(paths *Paths, cfg *config.Config) error {
	if cfg.Net.Mode == "unjailed" {
		// Mounting /sys is allowed only within own network namespace
		return nil
	}
	flags := uintptr(syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV)
	if err := mount("sysfs", paths.FsRoot+"/sys", "sysfs",
		flags, ""); err != nil {
		return fmt.Errorf("mount /sys failed: %v", err)
	}
	return nil
}

func mountVar(paths *Paths) error {
	flags := uintptr(syscall.MS_NOSUID)
	if err := bindDir(paths.Var, paths.FsRoot+"/var", flags); err != nil {
		return fmt.Errorf("mount /var failed: %v", err)
	}
	return nil
}

// blockFsRootEntries blocks access to file system entries by bind
// mounting empty files/directories over them.
func blockFsRootEntries(paths *Paths, entries []string) error {
	for _, blockedPath := range entries {
		fullPath := filepath.Join(paths.FsRoot, blockedPath)
		info, err := os.Stat(fullPath)
		if os.IsNotExist(err) {
			// Path doesn't exist, nothing to block
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to stat %s: %v", fullPath, err)
		}

		if info.IsDir() {
			if err := bindDir(paths.EmptyDir, fullPath, syscall.MS_RDONLY); err != nil {
				return fmt.Errorf("failed to block directory %s: %v", blockedPath, err)
			}
		} else {
			if err := bindFile(paths.EmptyFile, fullPath, syscall.MS_RDONLY); err != nil {
				return fmt.Errorf("failed to block file %s: %v", blockedPath, err)
			}
		}
	}
	return nil
}

// pivotRoot changes root to be paths.FsRoot and unmount the original
// mount tree.
func pivotRoot(paths *Paths) error {
	flags := uintptr(syscall.MS_NOSUID | syscall.MS_REC | syscall.MS_RDONLY | syscall.MS_PRIVATE)
	// Pivot root new root dir must be a mount point, but paths.FsRoot
	// is a normal dir, so bind mount it to itself. This also makes
	// it read-only, which is preferred.
	if err := bindDir(paths.FsRoot, paths.FsRoot, flags); err != nil {
		return fmt.Errorf("failed to mount %s: %v", paths.FsRoot, err)
	}

	tmpDir := os.TempDir()
	oldRoot := filepath.Join(paths.FsRoot, tmpDir)
	// tmp dir will point to the old root, and then the old root is unmounted.
	if err := syscall.PivotRoot(paths.FsRoot, oldRoot); err != nil {
		return fmt.Errorf("pivot root to %s failed: %v", paths.FsRoot, err)
	}
	// Unmounting of the whole root is allowed, only unmounting
	// individual submounts is not permitted.
	if err := syscall.Unmount(tmpDir, syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("unmounting root filesystem failed: %v", err)
	}

	return nil
}
