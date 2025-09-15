package jailfs

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
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
func NewPaths(jailId string, configPath string) (*Paths, error) {
	hostHome, err := homeDir()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	dotdir := filepath.Join(hostHome, ".dirjail")
	base := filepath.Join(dotdir, "jails", jailId)

	if configPath == "" {
		configPath = filepath.Join(dotdir, "config")
	}
	internal := filepath.Join(dotdir, "internal")
	run, err := initRunDir(internal, jailId)
	if err != nil {
		return nil, err
	}

	paths := Paths{
		Cwd:       cwd,
		DotDir:    dotdir,
		Config:    configPath,
		Base:      base,
		FsRoot:    filepath.Join(run, "root"),
		HostHome:  hostHome,
		Home:      filepath.Join(base, "home"),
		Etc:       filepath.Join(base, "etc"),
		Run:       run,
		EmptyDir:  filepath.Join(internal, "emptyd"),
		EmptyFile: filepath.Join(internal, "empty"),
	}

	toMkdir := []string{paths.FsRoot, paths.Home, paths.Etc}
	for _, dir := range toMkdir {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %v", dir, err)
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

func homeDir() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %v", err)
	}
	return currentUser.HomeDir, nil
}

func initRunDir(jailId string, internalDir string) (string, error) {
	runParent := filepath.Join(internalDir, "run")
	if err := os.MkdirAll(runParent, 0700); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %v", runParent, err)
	}
	path, err := os.MkdirTemp(runParent, fmt.Sprintf("%s-", jailId))
	if err != nil {
		return "", fmt.Errorf("failed to create run sub-directory: %v", err)
	}
	return path, nil
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

func initTmpSubDir(jailId string, paths *Paths) (string, error) {
	dirNameFile := filepath.Join(paths.Base, ".tmp")

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
						return tmpSubDir, nil
					}
				}
			}
		}
	}

	// New tmp sub directory is needed.
	tmpSubDir, err := os.MkdirTemp("", tmpDirName(jailId))
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %v", err)
	}
	dirName := filepath.Base(tmpSubDir)

	// Write the directory name to the file, so the dir can be re-used by
	// other dirjails with the same id
	if err := os.WriteFile(dirNameFile, []byte(dirName), 0600); err != nil {
		return "", fmt.Errorf("failed to write to %v: %v", dirNameFile, err)
	}
	return tmpSubDir, nil
}

func tmpDirName(jailId string) string {
	return "dirjail-" + jailId + "-"
}
