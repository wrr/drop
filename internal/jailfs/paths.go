package jailfs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Paths contains filesystem paths used to setup the jail.
type Paths struct {
	// Cwd is the directory where Drop was started.
	Cwd string
	// DotDir is the top-level directory where Drop files are stored
	// (e.g. /home/alice/.drop).
	DotDir string
	// Env is the entry point for all paths specific to the current
	// environment. For example, if envId is 'project-foo', Env is
	// /home/alice/.drop/envs/project-foo.
	Env string
	// FsRoot is where the root filesystem is assembled before chroot.
	FsRoot string
	// HostHome is the user's home directory on the host system
	// (e.g. /home/alice).
	HostHome string
	// Home is the directory mounted as the home directory in the jail
	// (e.g. /home/alice/.drop/envs/project-foo/home).
	Home string
	// Etc is the directory mounted as read-only overlay over /etc in the jail
	// (e.g. /home/alice/.drop/envs/project-foo/etc).
	Etc string
	// Var is the directory mounted as /var in the jail. The original
	// /var is hidden
	// (e.g. /home/alice/.drop/envs/project-foo/var).
	Var string
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

// NewPaths creates Paths object with the relevant paths for the
// current environment and creates missing dir and files.
func NewPaths(envId string, hostHome string, runDir string) (*Paths, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	dotDir := filepath.Join(hostHome, ".drop")
	env := filepath.Join(dotDir, "envs", envId)
	internal := filepath.Join(dotDir, "internal")

	paths := Paths{
		Cwd:       cwd,
		DotDir:    dotDir,
		Env:       env,
		FsRoot:    filepath.Join(runDir, "root"),
		HostHome:  hostHome,
		Home:      filepath.Join(env, "home"),
		Etc:       filepath.Join(env, "etc"),
		Var:       filepath.Join(env, "var"),
		Run:       runDir,
		EmptyDir:  filepath.Join(internal, "emptyd"),
		EmptyFile: filepath.Join(internal, "empty"),
	}

	toMkdir := []string{paths.FsRoot, paths.Home, paths.Etc, paths.Var}
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

	tmp, err := initTmpSubDir(envId, &paths)
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
func NewRunDir(homeDir string, envId string) (string, error) {
	parent := filepath.Join(homeDir, ".drop", "internal", "run")

	if err := MkdirAll(parent); err != nil {
		return "", err
	}
	path, err := os.MkdirTemp(parent, fmt.Sprintf("%s-", envId))
	if err != nil {
		return "", fmt.Errorf("failed to create run sub-directory: %v", err)
	}
	return path, nil
}

func DefaultConfigPath(hostHome string) string {
	return filepath.Join(hostHome, ".drop", "config")
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

var envIdChars = `a-zA-Z0-9-_\.`

func IsEnvIdValid(envId string) bool {
	reg := regexp.MustCompile(`^[` + envIdChars + `]+$`)
	// Do not allow '-' and '.' at the start, because directory created
	// for this environment will then be tricky to handle with standard
	// shell tools (directory name interpreted as a command flag or a
	// hidden dir).
	return len(envId) > 0 && envId[0] != '-' && envId[0] != '.' && reg.MatchString(envId)
}

func CwdToEnvId() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory failed: %v", err)
	}
	return pathToEnvId(cwd), nil
}

func pathToEnvId(path string) string {
	dname := strings.ReplaceAll(path, "/", "-")
	// remove all leading '-' and '.'
	dname = strings.TrimLeft(dname, ".-")
	// remove all trailing '-'
	dname = strings.TrimRight(dname, "-")
	if len(dname) == 0 {
		return "root"
	}
	// Keep only allowed env ID characters
	reg := regexp.MustCompile(`[^` + envIdChars + `]`)
	return reg.ReplaceAllString(dname, "_")
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

// initTmpSubDir checks if a tmp sub directory for the current
// environment already exists and has correct owner and
// permissions. If this is not the case, it it creates a new sub
// directory in tmp.
//
// In order to keep track of already existing tmp sub directory, a
// link in the Env directory is created that points to it.
//
// The function returns a path to the tmp subdirectory
func initTmpSubDir(envId string, paths *Paths) (string, error) {
	tmpSymlink := filepath.Join(paths.Env, "tmp")

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

	tmpSubDir, err := os.MkdirTemp("", tmpDirName(envId))
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %v", err)
	}

	// Create symbolic link to the tmp directory
	if err := os.Symlink(tmpSubDir, tmpSymlink); err != nil {
		return "", fmt.Errorf("failed to create symlink: %v", err)
	}
	return tmpSubDir, nil
}

func tmpDirName(envId string) string {
	return "drop-" + envId + "-"
}
