package jailfs

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"syscall"
)

// Paths contains filesystem paths used to setup the jail.
type Paths struct {
	// Cwd is the directory where dirjail was started.
	Cwd string
	// DotDir is the top-level directory where dirjail files are stored
	// (e.g. /home/alice/.dirjail).
	DotDir string
	// Config is the path to the dirjail configuration file.
	Config string
	// Base is the entry point for all paths specific to the current jail ID.
	// For example, if jail-id is 'project-foo', Base is
	// /home/alice/.dirjail/jails/project-foo.
	Base string
	// FsRoot is where the jail's root filesystem is assembled before chroot.
	FsRoot string
	// HostHome is the user's home directory on the host system
	// (e.g. /home/alice).
	HostHome string
	// Home is the directory mounted as the home directory in the jail
	// (e.g. /home/alice/.dirjail/jails/project-foo/home).
	Home string
	// Etc is the directory mounted as read-only overlay over /etc in the jail
	// (e.g. /home/alice/.dirjail/jails/project-foo/etc).
	Etc string
	// Tmp is the directory mounted as /tmp in the jail. It is placed as a
	// subdir of the host $TMPDIR to allow standard cleanup mechanisms.
	Tmp string
	// Run holds temporary files and dirs for the current jail instance
	// (for example home dir overlayfs lower and work dirs). It can be
	// safely remove once the jailed process terminates.
	Run string
	// EmptyDir is an empty directory used to hide directories in the jail.
	EmptyDir string
	// EmptyFile is an empty file used to hide files in the jail.
	EmptyFile string
}

// NewPaths populates Paths with the relevant paths for the current
// jail and creates missing dir and files.
func NewPaths(jailId string, configPath string, runDir string) (*Paths, error) {
	hostHome, err := homeDir()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	dotDir := filepath.Join(hostHome, ".dirjail")
	base := filepath.Join(dotDir, "jails", jailId)

	if configPath == "" {
		configPath = filepath.Join(dotDir, "config")
	}
	internal := filepath.Join(dotDir, "internal")

	paths := Paths{
		Cwd:       cwd,
		DotDir:    dotDir,
		Config:    configPath,
		Base:      base,
		FsRoot:    filepath.Join(runDir, "root"),
		HostHome:  hostHome,
		Home:      filepath.Join(base, "home"),
		Etc:       filepath.Join(base, "etc"),
		Run:       runDir,
		EmptyDir:  filepath.Join(internal, "emptyd"),
		EmptyFile: filepath.Join(internal, "empty"),
	}

	toMkdir := []string{paths.FsRoot, paths.Home, paths.Etc}
	for _, dir := range toMkdir {
		if err := MkdirAll(dir); err != nil {
			return nil, err
		}
	}

	if err := ensureDirWithNoPerms(paths.EmptyDir); err != nil {
		return nil, err
	}
	if err := ensureEmptyFile(paths.EmptyFile); err != nil {
		return nil, err
	}

	tmp, err := initTmpSubDir(jailId, &paths)
	if err != nil {
		return nil, err
	}
	paths.Tmp = tmp
	return &paths, nil
}

// NewRunDir creates a directory to store this jail instance runtime
// files and dirs (for example, the main root file system mount
// point). The directory can be removed when this jail instance
// terminates.
func NewRunDir(jailId string) (string, error) {
	hostHome, err := homeDir()
	if err != nil {
		return "", err
	}
	parent := filepath.Join(hostHome, ".dirjail", "internal", "run")

	if err := MkdirAll(parent); err != nil {
		return "", err
	}
	path, err := os.MkdirTemp(parent, fmt.Sprintf("%s-", jailId))
	if err != nil {
		return "", fmt.Errorf("failed to create run sub-directory: %v", err)
	}
	return path, nil
}

// ClearRunDir removes the jail instance specific runtime files, no longer
// needed when jail is exited.
func CleanRunDir(path string) error {
	return os.RemoveAll(path)
}

// MkdirAll is a simple wrapper over os.MkdirAll. It always uses
// permissions 0700 and returns verbose error which can be propagated
// up without adding an aditional context.
func MkdirAll(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", path, err)
	}
	return nil
}
func homeDir() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %v", err)
	}
	return currentUser.HomeDir, nil
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

// initTmpSubDir checks if a tmp sub directory for the current jail
// already exists and has correct owner and permissions. If this
// is not the case, it it creates a new sub directory in tmp.
//
// In order to keep track of already existing tmp sub directory, a
// link in this jail base directory is created that points to it.
//
// The function returns a path to the tmp subdirectory
func initTmpSubDir(jailId string, paths *Paths) (string, error) {
	tmpSymlink := filepath.Join(paths.Base, "tmp")

	if target, err := os.Readlink(tmpSymlink); err == nil {
		// No error - symlink exists
		tmpSubDir := target

		if stat, err := os.Stat(tmpSubDir); err == nil && stat.IsDir() {
			// Check if directory exists, is owned by current user, and has 700 permissions
			if sysStats, ok := stat.Sys().(*syscall.Stat_t); ok {
				currentUID := os.Getuid()
				if int(sysStats.Uid) == currentUID && stat.Mode().Perm() == 0700 {
					return tmpSubDir, nil
				}
			}
		}
		// Target directory is missing or invalid, remove the symlink, and
		// create a new tmp sub dir.
		os.Remove(tmpSymlink)
	}

	tmpSubDir, err := os.MkdirTemp("", tmpDirName(jailId))
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %v", err)
	}

	// Create symbolic link to the tmp directory
	if err := os.Symlink(tmpSubDir, tmpSymlink); err != nil {
		return "", fmt.Errorf("failed to create symlink: %v", err)
	}
	return tmpSubDir, nil
}

func tmpDirName(jailId string) string {
	return "dirjail-" + jailId + "-"
}
