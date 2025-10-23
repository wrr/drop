package jailfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/log"
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
// We skip this step, Linux already makes these paths writable by root
// only, perhaps this is needed in case container is started as root,
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
		return fmt.Errorf("mount %v to %v failed: %v", src, absTrg, err)
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
		return fmt.Errorf("bind %v", err)
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

func (rt *root) bindAll(mounts []config.Mount, flags uintptr) error {
	infoHeader := "not mounting %s, target already exists "
	for _, m := range mounts {
		src := m.Source
		trg := m.Target
		rdonly := uintptr(0)
		if !m.RW {
			rdonly |= unix.MS_RDONLY
		}
		hostTrg := filepath.Join(rt.fsRoot, trg)

		if srcInfo, err := os.Stat(src); err == nil {

			trgInfo, _ := os.Lstat(hostTrg)
			if trgInfo != nil && trgInfo.Mode()&os.ModeSymlink != 0 {
				log.Info(infoHeader+"but is a symbolic link", src)
				continue
			}
			if srcInfo.IsDir() {
				if trgInfo != nil && !trgInfo.IsDir() {
					log.Info(infoHeader+"but is a file not a directory", src)
					continue
				}
				if err := rt.bind(src, trg, flags|rdonly); err != nil {
					return err
				}
			} else {
				if trgInfo != nil && !trgInfo.Mode().IsRegular() {
					log.Info(infoHeader+"but is a directory not a file", src)
					continue
				}
				if err := rt.bindFile(src, trg, flags|rdonly); err != nil {
					return err
				}
			}
		} else {
			log.Info("not mounting %s, no such file or directory", src)
		}
	}
	return nil
}

// mountRootSubDirs bind mounts all exposed sub-dirs of /.
func (rt *root) mountRootSubDirs() error {
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
func (rt *root) mountHome(paths *Paths) error {
	// Mount home dir as overlayfs, lowerdir holds only mount points,
	// upperdir is where the actual files are stored.
	// xino=off to disable Ubuntu 24.04 dmesg warning 'overlayfs: fs on
	// '/home/...' does not support file handles, falling back to
	// xino=off'. It is very unlikely for home dir overlayfs layers to be on
	// different filesystems (~/.drop dir would need to be placed by the
	// user on a different filesystem), when layers are on the same fs
	// xino=on option does nothing.
	// https://docs.kernel.org/filesystems/overlayfs.html
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,xino=off", paths.HomeLower, paths.Home, paths.HomeWork)
	flags := uintptr(unix.MS_NOSUID)
	return rt.mount("home", paths.HostHome, "overlay", flags, opts)
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
	return rt.mount("etc", "/etc", "overlay", flags, opts)
}

func (rt *root) mountRun() error {
	flags := uintptr(unix.MS_NOEXEC | unix.MS_NOSUID | unix.MS_NODEV)
	return rt.mount("run", "/run", "tmpfs", flags, "mode=700")
}

func (rt *root) mountDev() error {
	if err := rt.mount("dev", "/dev", "tmpfs", unix.MS_NOSUID, "mode=700"); err != nil {
		return err
	}
	flags := uintptr(unix.MS_NOEXEC | unix.MS_NOSUID | unix.MS_NODEV)
	if err := rt.mount("shm", "/dev/shm", "tmpfs", flags, "mode=1700"); err != nil {
		return err
	}

	// mkdev is not allowed in the container when running as a user,
	// even if unix.CAP_MKNOD is passed, so we map some host devices to
	// the container /dev instead.
	devices := []config.Mount{
		{Source: "/dev/null", Target: "/dev/null"},
		{Source: "/dev/zero", Target: "/dev/zero"},
		{Source: "/dev/full", Target: "/dev/full"},
		{Source: "/dev/random", Target: "/dev/random"},
		{Source: "/dev/urandom", Target: "/dev/urandom"},
	}
	flags = uintptr(unix.MS_NOEXEC | unix.MS_NOSUID)
	if err := rt.bindAll(devices, flags); err != nil {
		return err
	}

	opts := "mode=600,newinstance,ptmxmode=666"
	flags = uintptr(unix.MS_NOEXEC | unix.MS_NOSUID)
	if err := rt.mount("devpts", "/dev/pts", "devpts", flags, opts); err != nil {
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

func (rt *root) mountTmp(paths *Paths) error {
	return rt.bind(paths.Tmp, os.TempDir(), 0)
}

func (rt *root) mountProc() error {
	flags := uintptr(unix.MS_NOEXEC | unix.MS_NOSUID | unix.MS_NODEV)
	return rt.mount("proc", "/proc", "proc", flags, "")
}

func (rt *root) mountSys(cfg *config.Config) error {
	if cfg.Net.Mode == "unjailed" {
		// Mounting /sys is allowed only within own network namespace
		return nil
	}
	flags := uintptr(unix.MS_NOEXEC | unix.MS_NOSUID | unix.MS_NODEV | unix.MS_RDONLY)
	if err := rt.mount("sysfs", "/sys", "sysfs", flags, ""); err != nil {
		return err
	}
	if err := rt.mount("cgroup", "/sys/fs/cgroup", "cgroup2", flags, ""); err != nil {
		return err
	}
	return nil
}

func (rt *root) mountVar(paths *Paths) error {
	flags := uintptr(unix.MS_NOSUID | unix.MS_NODEV)
	return rt.bind(paths.Var, "/var", flags)
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
		flags := uintptr(unix.MS_NOEXEC | unix.MS_NOSUID | unix.MS_RDONLY)
		if info.IsDir() {
			if err := rt.bind(paths.EmptyDir, blockedPath, flags); err != nil {
				return fmt.Errorf("failed to block directory %s: %v", blockedPath, err)
			}
		} else {
			if err := rt.bindFile(paths.EmptyFile, blockedPath, flags); err != nil {
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

	if err := rt.mountRootSubDirs(); err != nil {
		return err
	}

	if err := rt.mountRun(); err != nil {
		return err
	}

	if err := rt.mountDev(); err != nil {
		return err
	}

	if err := rt.mountTmp(paths); err != nil {
		return err
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

	// Resolve home directory in all mounts and separate by RW flag
	mounts := resolveHomeDir(cfg.Mounts, paths.HostHome)
	// Mount current working directory and it's subdirs as readable and
	// writable, but only if Cwd is not the home directory or a parent of it
	// to avoid exposing the original home directory.
	if !isSubDirOrSame(paths.Cwd, paths.HostHome) {
		mounts = append(mounts, config.Mount{Source: paths.Cwd, Target: paths.Cwd, RW: true})
	}

	// This need to be done before overlayfs are mounted (/etc and user home).
	if err := createOverlayFSMountPoints(mounts, paths); err != nil {
		return err
	}
	if err := rt.mountHome(paths); err != nil {
		return err
	}

	if err := rt.mountEtc(paths); err != nil {
		return err
	}

	// MS_REC is required, see the comment in mountRootSubDirs
	flags := uintptr(unix.MS_NOSUID | unix.MS_REC | unix.MS_PRIVATE)
	if err := rt.bindAll(mounts, flags); err != nil {
		return err
	}

	// Combine always blocked paths with user-configured blocked paths
	blockedPaths := append(alwaysBlocked, cfg.Blocked...)
	if err := rt.blockEntries(paths, blockedPaths); err != nil {
		return err
	}

	return rt.pivot()
}

// resolveHomeDir returns a copy of mounts with ~/ in Source and
// Target paths replaced with the homeDir.
func resolveHomeDir(mounts []config.Mount, homeDir string) []config.Mount {
	out := make([]config.Mount, len(mounts))
	for i, m := range mounts {
		out[i] = m
		out[i].Source = osutil.TildeToHomeDir(m.Source, homeDir)
		out[i].Target = osutil.TildeToHomeDir(m.Target, homeDir)
	}
	return out
}

// getOverlayFSMountPointPath returns a path where mount point for trg
// should be created, but only if this mouint point is on overlayFS.
// If the mount point is not on overlayFS, the functions returns "".
func getOverlayFSMountPointPath(trg string, paths *Paths) string {
	homeRel, err := filepath.Rel(paths.HostHome, trg)
	if err != nil && !strings.HasPrefix(homeRel, "..") {
		return filepath.Join(paths.HomeLower, homeRel)
	}
	etcRel, err := filepath.Rel("/etc", trg)
	if err != nil && !strings.HasPrefix(etcRel, "..") {
		return filepath.Join(paths.Etc, etcRel)
	}
	return ""
}

// createOverlayFSMountPoints creates mount points on homedir and
// /etc/ overlayfs layers before overlayfs are mounted.
//
// For /etc this is needed, because /etc upper layer (controlled by
// the user) is mounted read only, so it is not possible to create
// missing sub-mount after /etc is mounted.
//
// For homedir, this allows to create missing mount points in the
// disposable lower dir, and do no pollute the actual Drop home dir
// with mount points.
//
// The function is best-effort, all errors are ignored. If mount
// points are not created, the actual mounting action will try to
// create the endpoint again and will report the error.
//
// Note that created endpoints may not be actually used in case other
// mounts shadow them.
func createOverlayFSMountPoints(mounts []config.Mount, paths *Paths) error {
	for _, m := range mounts {
		ovrlTrg := getOverlayFSMountPointPath(m.Target, paths)
		if ovrlTrg == "" {
			// Target not on overlayfs (or some error).
			continue
		}
		if info, err := os.Stat(m.Source); err == nil {
			if info.IsDir() {
				osutil.MkdirAll(ovrlTrg)
			} else {
				createEmptyFile(ovrlTrg)
			}
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
