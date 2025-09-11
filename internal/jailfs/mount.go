package jailfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"

	"github.com/wrr/dirjail/internal/config"
)

func createEmptyFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL, 0000)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}
	return file.Close()
}

func doBind(src, dst string, mountflags uintptr) error {
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

func bindDir(src, dst string, mountflags uintptr) error {
	if err := os.MkdirAll(dst, 0700); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dst, err)
	}
	return doBind(src, dst, mountflags)
}

func bindFile(src, dst string, mountflags uintptr) error {
	dstParent := filepath.Dir(dst)
	if err := os.MkdirAll(dstParent, 0700); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dstParent, err)
	}
	// Mount destination must exist, create an empty file to be the
	// destination mount point
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := createEmptyFile(dst); err != nil {
			return err
		}
	}
	return doBind(src, dst, mountflags)
}

func bindEntries(srcDir, dstDir string, entries []string, readonly bool) error {
	mountflags := uintptr(syscall.MS_BIND)
	if readonly {
		mountflags |= syscall.MS_RDONLY
	}

	for _, entry := range entries {
		entryPath := filepath.Join(srcDir, entry)
		newEntryPath := filepath.Join(dstDir, entry)

		if info, err := os.Stat(entryPath); err == nil {
			if info.IsDir() {
				if err := bindDir(entryPath, newEntryPath, mountflags); err != nil {
					return err
				}
			} else {
				if err := bindFile(entryPath, newEntryPath, mountflags); err != nil {
					return err
				}
			}
		} else {
			fmt.Printf("Not mounting %s, no such file or directory\n", entryPath)
		}
	}
	return nil
}

var digitsRegex = regexp.MustCompile(`^\d+$`)

func allDigits(s string) bool {
	return digitsRegex.MatchString(s)
}

func hideProcFiles(procAccessible []string, paths *Paths) error {
	procRoot := paths.FsRoot + "/proc"
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return fmt.Errorf("failed to read %v: %v", procRoot, err)
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
			return fmt.Errorf("failed to retrieve file info for %s %v", fullPath, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			// skip symlinks such as /proc/self
		} else if info.IsDir() {
			if err := bindDir(paths.EmptyDir, fullPath, syscall.MS_BIND|syscall.MS_RDONLY); err != nil {
				return err
			}
		} else {
			if err := bindFile(paths.EmptyFile, fullPath, syscall.MS_BIND|syscall.MS_RDONLY); err != nil {
				return err
			}
		}
	}
	return nil
}

// ArrangeFilesystem sets up the jail filesystem chierarchy.  It mounts
// root as read only, mounts a dedicated home directory with only some
// entries from the host home dir exposed. Creates overlay for /etc,
// sets up tmpfs for /run and /dev, exposes some device files from the
// host, sets up /proc, and hides most of the proc entries.
func ArrangeFilesystem(paths *Paths, cfg *config.Config) error {
	if err := bindEntries(paths.HostHome, paths.Home, cfg.HomeVisible, true); err != nil {
		return err
	}
	if err := bindEntries(paths.HostHome, paths.Home, cfg.HomeWriteable, false); err != nil {
		return err
	}

	if err := bindDir("/", paths.FsRoot, syscall.MS_BIND|syscall.MS_REC|syscall.MS_RDONLY); err != nil {
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
	if err := syscall.Mount("overlay", paths.FsRoot+"/etc", "overlay", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_RDONLY, opts); err != nil {
		return fmt.Errorf("mount /etc failed: %v", err)
	}

	if err := syscall.Mount("", paths.FsRoot+"/run", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
		return fmt.Errorf("mount /run failed: %v", err)
	}

	if err := syscall.Mount("", paths.FsRoot+"/dev", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID, "mode=755"); err != nil {
		return fmt.Errorf("mount /dev failed: %v", err)
	}

	if err := os.Mkdir(paths.FsRoot+"/dev/shm", 0700); err != nil {
		return err
	}
	if err := syscall.Mount("", paths.FsRoot+"/dev/shm", "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, "mode=1777"); err != nil {
		return fmt.Errorf("mount /dev/shm failed: %v", err)
	}

	// mkdev is not allowed in the container when running as a user,
	// even if unix.CAP_MKNOD is passed, so we map some host devices to
	// the container /dev instead.
	devices := []string{"null", "zero", "full", "random", "urandom"}
	if err := bindEntries("/dev", paths.FsRoot+"/dev", devices, false); err != nil {
		return err
	}

	if err := bindEntries("/dev", paths.FsRoot+"/dev/test/", devices, false); err != nil {
		return err
	}

	if err := os.Mkdir(paths.FsRoot+"/dev/pts", 0700); err != nil {
		return err
	}
	if err := syscall.Mount("", paths.FsRoot+"/dev/pts", "devpts", syscall.MS_NOEXEC|syscall.MS_NOSUID, ""); err != nil {
		return fmt.Errorf("mount /dev/pts failed: %v", err)
	}

	homeDst := filepath.Join(paths.FsRoot, paths.HostHome)
	if err := bindDir(paths.Home, homeDst, syscall.MS_BIND|syscall.MS_REC); err != nil {
		return err
	}

	tmpDst := filepath.Join(paths.FsRoot, os.TempDir())
	if err := bindDir(paths.Tmp, tmpDst, syscall.MS_BIND); err != nil {
		return err
	}

	// Mount current working directory
	if err := bindDir(paths.Cwd, filepath.Join(paths.FsRoot, paths.Cwd), syscall.MS_BIND|syscall.MS_REC); err != nil {
		return err
	}

	if err := syscall.Mount("", paths.FsRoot+"/proc", "proc", 0, ""); err != nil {
		return fmt.Errorf("mount proc failed: %v", err)
	}
	if err := hideProcFiles(cfg.ProcReadable, paths); err != nil {
		return err
	}

	return nil
}
