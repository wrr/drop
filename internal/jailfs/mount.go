package jailfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

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

// root provides operations to assemble the new root filesystem.
// All mounting operations targets are relative to the fsRoot (new
// root).
type root struct {
	// Path where new root filesystem is assembled. Once assembled,
	// pivot() switches root.
	fsRoot string
}

// mount is the entry point for all mount calls that assemble the root
// filesystem. target argument is relative to FsRoot. mount ensures
// that trg exists, if it is missing creates directory with
// permissions 0700 at the target.
func (rt *root) mount(src, trg, fstype string, flags uintptr, data string) (err error) {
	absTrg := filepath.Join(rt.fsRoot, trg)
	if !osutil.Exists(absTrg) {
		if err := osutil.MkdirAll(absTrg); err != nil {
			return err
		}
	}
	if err := unix.Mount(src, absTrg, fstype, flags, data); err != nil {
		return fmt.Errorf("mount %v failed: %v", trg, err)
	}
	return nil
}

func (rt *root) bindFile(src, trg string, mountflags uintptr) error {
	absTrg := filepath.Join(rt.fsRoot, trg)
	// Mount destination must exist, create an empty file to be the
	// destination mount point
	if _, err := os.Stat(absTrg); os.IsNotExist(err) {
		if err := createEmptyFile(absTrg); err != nil {
			return err
		}
	}
	return rt.bind(src, trg, mountflags)
}

// Bind binds src dir (absolute host path) to trg dir (path relative
// to fsRoot or absolute guest path).
// If mount flags includes MS_RDONLY, bind ensures the trg entry and
// all its submounts are read-only.
func (rt *root) bind(src, trg string, mountflags uintptr) error {
	mountflags |= unix.MS_BIND
	if err := rt.mount(src, trg, "", mountflags, ""); err != nil {
		return fmt.Errorf("mount %s to %s failed: %v", src, trg, err)
	}
	absTrg := filepath.Join(rt.fsRoot, trg)
	// mount and remount is needed for RDONLY to work:
	// https://github.com/containerd/containerd/issues/1368
	// but even then RDONLY applies only to the top level mount.
	// We use setaatr instead to apply RDONLY recursievly to all submounts.
	// (requires kernel >= 5.12)
	if mountflags&unix.MS_RDONLY != 0 {
		attr := &unix.MountAttr{
			Attr_set: unix.MOUNT_ATTR_RDONLY,
		}
		if err := unix.MountSetattr(-1, absTrg, unix.AT_RECURSIVE, attr); err != nil {
			return fmt.Errorf("setting mount %s readonly failed: %v", trg, err)
		}
	}
	return nil
}

func (rt *root) bindAll(srcDir, trgDir string, entries []string, readonly bool) error {
	flags := uintptr(0)
	if readonly {
		flags |= unix.MS_RDONLY
	}

	for _, entry := range entries {
		src := filepath.Join(srcDir, entry)
		trg := filepath.Join(trgDir, entry)

		if info, err := os.Stat(src); err == nil {
			if info.IsDir() {
				if err := rt.bind(src, trg, flags); err != nil {
					return err
				}
			} else {
				if err := rt.bindFile(src, trg, flags); err != nil {
					return err
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "Not mounting %s, no such file or directory\n", src)
		}
	}
	return nil
}

// mountRootSubDir bind mounts all exposed sub-dirs of /.
func (rt *root) mountRootSubDir() error {
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
	flags := uintptr(unix.MS_NOSUID | unix.MS_REC | unix.MS_RDONLY | unix.MS_PRIVATE)
	dirs := []string{"/usr", "/bin", "/lib", "/lib32", "/lib64", "/sbin"}

	for _, dir := range dirs {
		info, err := os.Lstat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to stat %s: %v", dir, err)
		}

		dstDir := filepath.Join(rt.fsRoot, dir)

		if info.Mode()&os.ModeSymlink != 0 {
			// dir is a symbolic link, copy it (on many distros /bin /sbin
			// /lib* are just links to sub dirs of /usr)
			target, err := os.Readlink(dir)
			if err != nil {
				return fmt.Errorf("failed to read symlink %s: %v", dir, err)
			}
			if err := os.Symlink(target, dstDir); err != nil {
				return fmt.Errorf("failed to create symlink %s -> %s: %v", dstDir, target, err)
			}
		} else if info.IsDir() {
			if err := rt.bind(dir, dir, flags); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("%s is neither a directory nor a symbolic link", dir)
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
func (rt *root) mountHome(paths *Paths, cfg *config.Config) error {
	homeLower := filepath.Join(paths.Run, "home-lower")
	if err := osutil.MkdirAll(homeLower); err != nil {
		return err
	}
	homeWork := filepath.Join(paths.Run, "home-work")
	if err := osutil.MkdirAll(homeWork); err != nil {
		return err
	}
	hostHome := paths.HostHome
	mountPoints := append(cfg.HomeVisible, cfg.HomeWriteable...)
	if isSubDir(hostHome, paths.Cwd) {
		// If CWD is a subdir of home, a mountpoint for it is also needed,
		// as CWD is mounted read-write.
		cwdRelPath, err := filepath.Rel(hostHome, paths.Cwd)
		if err != nil {
			return err
		}
		mountPoints = append(mountPoints, cwdRelPath)
	}

	// Create empty files and dirs to be mount point in the overlayfs
	// lower dir.
	if err := createMountPoints(hostHome, homeLower, mountPoints); err != nil {
		return err
	}

	// Mount home dir as overlayfs, lowerdir holds only mount points,
	// upperdir is where the actual files are stored.
	// xino=off to disable Ubuntu 24.04 dmesg warning 'overlayfs: fs on
	// '/home/...' does not support file handles, falling back to
	// xino=off'. It is very unlikely for home dir overlayfs layers to be on
	// different filesystems (~/.drop dir would need to be placed by the
	// user on a different filesystem), when layers are on the same fs
	// xino=on option does nothing.
	// https://docs.kernel.org/filesystems/overlayfs.html
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,xino=off", homeLower, paths.Home, homeWork)
	if err := rt.mount("home", hostHome, "overlay", unix.MS_NOSUID, opts); err != nil {
		return err
	}

	if err := rt.bindAll(hostHome, hostHome, cfg.HomeVisible, true); err != nil {
		return err
	}
	if err := rt.bindAll(hostHome, hostHome, cfg.HomeWriteable, false); err != nil {
		return err
	}

	return nil
}

func (rt *root) mountEtc(paths *Paths) error {
	flags := uintptr(unix.MS_NOEXEC | unix.MS_NOSUID | unix.MS_NODEV | unix.MS_RDONLY)
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
	if err := rt.mount("etc", "/etc", "overlay", flags, opts); err != nil {
		return err
	}
	return nil
}

func (rt *root) mountRun() error {
	flags := uintptr(unix.MS_NOEXEC | unix.MS_NOSUID | unix.MS_NODEV)
	if err := rt.mount("run", "/run", "tmpfs", flags, "mode=700"); err != nil {
		return err
	}
	return nil
}

func (rt *root) mountDev() error {
	flags := uintptr(unix.MS_NOEXEC | unix.MS_NOSUID)
	if err := rt.mount("dev", "/dev", "tmpfs", flags, "mode=700"); err != nil {
		return err
	}
	if err := rt.mount("dev-shm", "/dev/shm", "tmpfs", flags|unix.MS_NODEV, "mode=1700"); err != nil {
		return err
	}

	// mkdev is not allowed in the container when running as a user,
	// even if unix.CAP_MKNOD is passed, so we map some host devices to
	// the container /dev instead.
	devices := []string{"null", "zero", "full", "random", "urandom"}
	if err := rt.bindAll("/dev", "/dev", devices, false); err != nil {
		return err
	}

	opts := "mode=600,newinstance,ptmxmode=600"
	if err := rt.mount("dev-pts", "/dev/pts", "devpts", flags, opts); err != nil {
		return err
	}

	symlinks := map[string]string{
		"ptmx":   "pts/ptmx",
		"stdin":  "/proc/self/fd/0",
		"stdout": "/proc/self/fd/1",
		"stderr": "/proc/self/fd/2",
		"fd":     "/proc/self/fd",
		"core":   "/proc/kcore",
	}
	devTrg := filepath.Join(rt.fsRoot, "/dev")
	for name, target := range symlinks {
		if err := os.Symlink(target, devTrg+"/"+name); err != nil {
			return fmt.Errorf("failed to create %s symlink: %v", name, err)
		}
	}

	return nil
}

func (rt *root) mountProc() error {
	if err := rt.mount("proc", "/proc", "proc", 0, ""); err != nil {
		return err
	}
	return nil
}

func (rt *root) mountSys(cfg *config.Config) error {
	if cfg.Net.Mode == "unjailed" {
		// Mounting /sys is allowed only within own network namespace
		return nil
	}
	flags := uintptr(unix.MS_NOEXEC | unix.MS_NOSUID | unix.MS_NODEV)
	if err := rt.mount("sysfs", "/sys", "sysfs",
		flags, ""); err != nil {
		return err
	}
	return nil
}

func (rt *root) mountVar(paths *Paths) error {
	flags := uintptr(unix.MS_NOSUID)
	if err := rt.bind(paths.Var, "/var", flags); err != nil {
		return err
	}
	return nil
}

// blockEntries blocks access to file system entries by bind
// mounting empty files/directories over them.
func (rt *root) blockEntries(paths *Paths, entries []string) error {
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
			if err := rt.bind(paths.EmptyDir, blockedPath, unix.MS_RDONLY); err != nil {
				return fmt.Errorf("failed to block directory %s: %v", blockedPath, err)
			}
		} else {
			if err := rt.bindFile(paths.EmptyFile, blockedPath, unix.MS_RDONLY); err != nil {
				return fmt.Errorf("failed to block file %s: %v", blockedPath, err)
			}
		}
	}
	return nil
}

// pivot changes root to be paths.FsRoot and unmounts the original
// mount tree.
func (rt *root) pivot() error {
	newRoot := rt.fsRoot
	flags := uintptr(unix.MS_BIND | unix.MS_NOSUID | unix.MS_REC | unix.MS_RDONLY | unix.MS_PRIVATE)
	// Pivot root new root dir must be a mount point, but paths.FsRoot
	// is a normal dir, so bind mount it to itself. This also makes
	// it read-only, which is preferred.
	//
	// We use rt.mount() directly instead of rt.bind(), because this is
	// the only case where we want the MS_RDONLY flag to apply only to
	// the root mount, not its submounts.
	if err := rt.mount(newRoot, "/", "", flags, ""); err != nil {
		return err
	}

	tmpDir := os.TempDir()
	oldRoot := filepath.Join(newRoot, tmpDir)
	// tmp dir will point to the old root, and then the old root is unmounted.
	if err := unix.PivotRoot(newRoot, oldRoot); err != nil {
		return fmt.Errorf("pivot root to %s failed: %v", newRoot, err)
	}
	// Unmounting of the whole root is allowed, only unmounting
	// individual submounts is not permitted.
	if err := unix.Unmount(tmpDir, unix.MNT_DETACH); err != nil {
		return fmt.Errorf("unmounting root filesystem failed: %v", err)
	}

	return nil
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
	attr := &unix.MountAttr{
		Propagation: unix.MS_PRIVATE,
	}
	if err := unix.MountSetattr(-1, "/", unix.AT_RECURSIVE, attr); err != nil {
		return fmt.Errorf("failed to set root filesystem propagation to private")
	}
	// Alternatively: unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, "");

	rt := &root{fsRoot: paths.FsRoot}

	if err := rt.mountRootSubDir(); err != nil {
		return err
	}

	if err := rt.mountHome(paths, cfg); err != nil {
		return err
	}

	if err := rt.mountEtc(paths); err != nil {
		return err
	}

	if err := rt.mountRun(); err != nil {
		return err
	}

	if err := rt.mountDev(); err != nil {
		return err
	}

	if err := rt.bind(paths.Tmp, os.TempDir(), 0); err != nil {
		return err
	}

	// Mount current working directory and it's subdirs as readable and
	// writable, but only if Cwd is not the home directory or a parent of it,
	// to avoid exposing the original home directory.
	if !isSubDirOrSame(paths.Cwd, paths.HostHome) {
		if err := rt.bind(paths.Cwd, paths.Cwd, 0); err != nil {
			return err
		}
	}

	if err := rt.mountProc(); err != nil {
		return err
	}

	if err := rt.mountSys(cfg); err != nil {
		return err
	}

	if err := rt.mountVar(paths); err != nil {
		return err
	}

	// Combine always blocked paths with user-configured blocked paths
	blockedPaths := append(alwaysBlocked, cfg.Blocked...)
	if err := rt.blockEntries(paths, blockedPaths); err != nil {
		return err
	}

	return rt.pivot()
}

func createMountPoints(srcDir, trgDir string, entries []string) error {
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry)
		trg := filepath.Join(trgDir, entry)

		if info, err := os.Stat(src); err == nil {
			// No error
			if info.IsDir() {
				if err := osutil.MkdirAll(trg); err != nil {
					return err
				}
			} else {
				if err := createEmptyFile(trg); err != nil {
					return err
				}
			}
		}
		// Do nothing: if file/directory doesn't exist in the srcDir, it cannot be
		// exposed with bind mount.
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
