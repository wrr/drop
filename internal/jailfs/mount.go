package jailfs

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/wrr/drop/internal/config"
)

// ArrangeFilesystem sets up the jail filesystem chierarchy.  It mounts
// root as read only, mounts a dedicated home directory with only some
// entries from the host home dir exposed. Creates overlay for /etc,
// sets up tmpfs for /run and /dev, exposes some device files from the
// host, sets up /proc, and hides most of the proc entries.
func ArrangeFilesystem(paths *Paths, cfg *config.Config) error {
	if err := bindDir("/", paths.FsRoot, syscall.MS_NOSUID|syscall.MS_REC|syscall.MS_RDONLY); err != nil {
		return err
	}
	if err := mountHome(paths, cfg); err != nil {
		return err
	}

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
	if err := syscall.Mount("etc", paths.FsRoot+"/etc", "overlay", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_RDONLY, opts); err != nil {
		return fmt.Errorf("mount /etc failed: %v", err)
	}

	if err := syscall.Mount("run", paths.FsRoot+"/run", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
		return fmt.Errorf("mount /run failed: %v", err)
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
	if err := MkdirAll(homeLower); err != nil {
		return err
	}
	homeWork := filepath.Join(paths.Run, "home-work")
	if err := MkdirAll(homeWork); err != nil {
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
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", homeLower, paths.Home, homeWork)
	if err := syscall.Mount("home", homeDst, "overlay", syscall.MS_NOSUID, opts); err != nil {
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
				if err := MkdirAll(dst); err != nil {
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

func mountDev(paths *Paths) error {
	flags := uintptr(syscall.MS_NOEXEC | syscall.MS_NOSUID)
	if err := syscall.Mount("dev", paths.FsRoot+"/dev", "tmpfs", flags, "mode=700"); err != nil {
		return fmt.Errorf("mount /dev failed: %v", err)
	}
	if err := os.Mkdir(paths.FsRoot+"/dev/shm", 0700); err != nil {
		return err
	}
	if err := syscall.Mount("dev-shm", paths.FsRoot+"/dev/shm", "tmpfs", flags|syscall.MS_NODEV, "mode=1700"); err != nil {
		return fmt.Errorf("mount /dev/shm failed: %v", err)
	}

	// mkdev is not allowed in the container when running as a user,
	// even if unix.CAP_MKNOD is passed, so we map some host devices to
	// the container /dev instead.
	devices := []string{"null", "zero", "full", "random", "urandom"}
	if err := bindAll("/dev", paths.FsRoot+"/dev", devices, false); err != nil {
		return err
	}

	if err := os.Mkdir(paths.FsRoot+"/dev/pts", 0700); err != nil {
		return err
	}
	opts := "mode=600,newinstance,ptmxmode=600"
	if err := syscall.Mount("dev-pts", paths.FsRoot+"/dev/pts", "devpts", flags, opts); err != nil {
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
		if err := os.Symlink(target, paths.FsRoot+"/dev/"+name); err != nil {
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
				if err := bindDir(src, dst, flags|syscall.MS_REC); err != nil {
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
	if err := MkdirAll(dst); err != nil {
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
	// https://github.com/opencontainers/runc/blob/675292473b3ad4c131b900806077148a556d78c9/libcontainer/rootfs_linux.go#L581
	if mountflags&syscall.MS_RDONLY != 0 {
		if err := syscall.Mount(dst, dst, "", syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_BIND, ""); err != nil {
			return fmt.Errorf("readonly re-mount of %s failed: %v", dst, err)
		}
	}
	return nil
}

// createEmptyFile creates an empty file and all its missing parent directories
func createEmptyFile(path string) error {
	parent := filepath.Dir(path)
	if err := MkdirAll(parent); err != nil {
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
	if err := syscall.Mount("proc", paths.FsRoot+"/proc", "proc", 0, ""); err != nil {
		return fmt.Errorf("mount proc failed: %v", err)
	}
	return hideProcEntries(paths)
}

// hideProcEntries hides the same /proc paths that runc default configuration
// hides. In addition runc configures the following paths to be
// read-only:
// "/proc/bus",
// "/proc/fs",
// "/proc/irq",
// "/proc/sys",
// "/proc/sysrq-trigger"
// We skip this step, because Linux already makes these paths
// read-only, perhaps this is needed in case container is started as root,
// which Drop doesn't allow.
func hideProcEntries(paths *Paths) error {
	hide := []string{
		"acpi",
		"asound",
		"kcore",
		"keys",
		"latency_stats",
		"timer_list",
		"timer_stats",
		"sched_debug",
		"scsi",
	}

	procRoot := paths.FsRoot + "/proc"
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return fmt.Errorf("failed to read %v: %v", procRoot, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if !slices.Contains(hide, name) {
			continue
		}
		info, err := entry.Info()
		fullPath := filepath.Join(procRoot, name)
		if err != nil {
			return fmt.Errorf("failed to retrieve file info for %s %v", fullPath, err)
		}
		if info.IsDir() {
			if err := bindDir(paths.EmptyDir, fullPath, syscall.MS_RDONLY); err != nil {
				return err
			}
		} else {
			if err := bindFile(paths.EmptyFile, fullPath, syscall.MS_RDONLY); err != nil {
				return err
			}
		}
	}
	return nil
}

func mountSys(paths *Paths, cfg *config.Config) error {
	if cfg.Net.Mode == "unjailed" {
		// Mounting /sys is allowed only within own network namespace
		return nil
	}

	flags := uintptr(syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV)
	if err := syscall.Mount("sysfs", paths.FsRoot+"/sys", "sysfs",
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
