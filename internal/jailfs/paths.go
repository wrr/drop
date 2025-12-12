// Copyright 2025 Jan Wrobel <jan@mixedbit.org>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package jailfs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/wrr/drop/internal/log"
	"github.com/wrr/drop/internal/osutil"
)

// Paths contains filesystem paths used to setup the jail.
type Paths struct {
	// Cwd is the directory where Drop was started.
	Cwd string
	// DropHome is the top-level directory where Drop files are stored
	// (e.g. /home/alice/.local/share/drop).
	DropHome string
	// Env is the entry point for all paths specific to the current
	// environment. For example, if envId is 'project-foo', Env is
	// /home/alice/.local/share/drop/envs/project-foo.
	Env string
	// FsRoot is where the root filesystem is assembled before chroot.
	FsRoot string
	// HostHome is the user's home directory on the host system
	// (e.g. /home/alice).
	HostHome string
	// Home is the directory mounted as the home directory in the jail
	// (e.g. /home/alice/.local/share/drop/envs/project-foo/home).
	Home string
	// Drop home dir can have entries exposed from the host home
	// directory via paths_ro, paths_rw config. To expose these entries
	// we need to create empty files and directories as mount points. In
	// order not to polute Drop home dir with these empty files and
	// dirs, we use overlayfs. Empty dirs and files are created in a
	// disposable lowerdir of the overlayfs (kept in the jails's 'run'
	// dir and removed when the jail terminates). The actual files
	// created in the jailed home are written to the overlayfs upper
	// layer.
	HomeLower string
	HomeWork  string
	// Etc is the directory mounted as read-only overlay over /etc in the jail
	// (e.g. /home/alice/.local/share/drop/envs/project-foo/etc).
	Etc string
	// Var is the directory mounted as /var in the jail. The original
	// /var is hidden
	// (e.g. /home/alice/.local/share/drop/envs/project-foo/var).
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
	dropHome, err := DropHome(hostHome)
	if err != nil {
		return nil, err
	}

	env := filepath.Join(dropHome, "envs", envId)
	internal := filepath.Join(dropHome, "internal")

	paths := Paths{
		Cwd:       cwd,
		DropHome:  dropHome,
		Env:       env,
		FsRoot:    filepath.Join(runDir, "root"),
		HostHome:  hostHome,
		Home:      filepath.Join(env, "home"),
		HomeLower: filepath.Join(runDir, "home-lower"),
		HomeWork:  filepath.Join(runDir, "home-work"),
		Etc:       filepath.Join(env, "etc"),
		Var:       filepath.Join(env, "var"),
		Run:       runDir,
		EmptyDir:  filepath.Join(internal, "emptyd"),
		EmptyFile: filepath.Join(internal, "empty"),
	}

	toMkdir := []string{paths.FsRoot, paths.Home, paths.HomeLower, paths.HomeWork, paths.Etc, paths.Var}
	for _, dir := range toMkdir {
		if err := osutil.MkdirAll(dir); err != nil {
			return nil, err
		}
	}

	if err := ensureDirWithNoPerms(paths.EmptyDir); err != nil {
		return nil, err
	}
	if err := ensureEmptyFile(paths.EmptyFile); err != nil {
		return nil, err
	}

	tmp, err := initEnvironmentTmpDir(envId, &paths)
	if err != nil {
		return nil, err
	}
	paths.Tmp = tmp
	return &paths, nil
}

// DefaultConfigPath returns default path to a Drop config file.
// This path is used when -config/-c option is not passed to Drop.
// If DROP_CONFIG is set, it is used directly, otherwise XDG specification is followed:
// the config is in (XDG_CONFIG_HOME or "~/.config")/drop/config.toml
func DefaultConfigPath(homeDir string) string {
	if path := os.Getenv("DROP_CONFIG"); path != "" {
		return path
	}
	parent := os.Getenv("XDG_CONFIG_HOME")
	if parent == "" {
		parent = filepath.Join(homeDir, ".config")
	}
	return filepath.Join(parent, "drop", "config.toml")
}

// DropHome returns the base directory for Drop data (environment
// dirs, internal files, such as mount points). If DROP_HOME is set,
// it is used directly, otherwise XDG specification is followed:
// (XDG_DATA_HOME or "~/.local/share")/drop/ is returned
func DropHome(homeDir string) (string, error) {
	if dropHome := os.Getenv("DROP_HOME"); dropHome != "" {
		if err := osutil.ValidateRootOrHomeSubPath(dropHome); err != nil {
			return "", fmt.Errorf("invalid DROP_HOME environment variable: %v", err)
		}
		return osutil.TildeToHomeDir(dropHome, homeDir), nil
	}
	parent := os.Getenv("XDG_DATA_HOME")
	if parent == "" {
		parent = filepath.Join(homeDir, ".local", "share")
	}
	dropHome := filepath.Join(parent, "drop")
	if err := osutil.ValidateRootOrHomeSubPath(dropHome); err != nil {
		return "", fmt.Errorf("invalid XDG_DATA_HOME environment variable: %v", err)
	}
	return dropHome, nil
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

func runDirsPath(dropHome string) string {
	return filepath.Join(dropHome, "internal", "run")
}

// NewRunDir creates a directory to store this jail instance runtime
// files and dirs (for example, the main root file system mount
// point). The directory can be removed when this jail instance
// terminates.
//
// We don't use XDG_RUNTIME_DIR, because it is commonly tmpfs and
// overlayfs mount points cannot be placed on it.
func NewRunDir(dropHome string, envId string) (string, func(), error) {
	parent := runDirsPath(dropHome)

	if err := osutil.MkdirAll(parent); err != nil {
		return "", nil, err
	}
	runDir, err := os.MkdirTemp(parent, fmt.Sprintf("%s-", envId))
	if err != nil {
		return "", nil, fmt.Errorf("failed to create run sub-directory: %v", err)
	}

	lockFile, err := lockRunDir(runDir)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		// Releases the lock (not crucial, because proccess termination
		// also does it).
		lockFile.Close()
		if err := cleanRunDir(runDir); err != nil {
			log.Info("failed to clean run dir %v", err)
		}
	}
	return runDir, cleanup, nil
}

// cleanRunDir removes runtime files no longer needed when Drop
// terminates.
func cleanRunDir(runDir string) error {
	// Remove the current instance run dir
	err := os.RemoveAll(runDir)
	if err != nil {
		return err
	}
	// This could be run at any time:
	return removeOrphanedRunDirs(filepath.Dir(runDir))
}

const runLockFname string = "lock"

// removeOrphanedRunDirs checks if run dirs orphaned by other Drop
// instances exist (orphaned dirs are created when Drop is killed
// with -9, system looses power, etc.). Removes them if they are older
// than orphanedRemoveAfter thredhols, this is to avoid race when freshly
// created, but not yet locked run dir would be removed.
func removeOrphanedRunDirs(runDirsPath string) error {
	orphanedRemoveAfter := 1 * time.Minute
	orphanedRemoveTime := time.Now().Add(-orphanedRemoveAfter)
	entries, err := os.ReadDir(runDirsPath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runDir := filepath.Join(runDirsPath, entry.Name())
		locked, err := isRunDirLocked(runDir)
		if err != nil {
			return err
		}
		if locked {
			// Still in use, not orphaned
			continue
		}
		info, err := os.Stat(runDir)
		if err != nil {
			return err
		}
		if info.ModTime().Before(orphanedRemoveTime) {
			if err := os.RemoveAll(runDir); err != nil {
				return err
			}
		}
	}
	return nil
}

// lockRunDir places a locked file in a run dir. The lock is
// automatically released by the kernel when process exits/dies. This
// allows to detect orphaned, unused run dirs and remove them.
func lockRunDir(runDir string) (*os.File, error) {
	lockPath := filepath.Join(runDir, runLockFname)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create a file %v: %v", lockPath, err)
	}
	// Do not close the file, as this releases the lock. The lock should
	// be released when the process terminates.

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return nil, fmt.Errorf("failed to lock a file %v: %v", lockPath, err)
	}
	return file, nil
}

func isRunDirLocked(runDir string) (bool, error) {
	lockPath := filepath.Join(runDir, runLockFname)
	file, err := os.Open(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Lock file doesn't exists, runDir is not locked
			return false, nil
		}
		return false, err
	}
	defer file.Close()
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// File is locked by another process or another error
		// (we just assume file is locked to avoid complex error
		//  handling).
		return true, nil
	}
	// Not locked, release the lock
	syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false, nil
}

func hasRunningDropInstances(runDirsPath string, envId string) (bool, error) {
	entries, err := os.ReadDir(runDirsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// dir doesn't exist, no running instances
			return false, nil
		}
		return false, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if this run directory belongs to the specified
		// environment. Run dirs have names: {envId}-{random digits}
		pattern := fmt.Sprintf(`^%s-\d+$`, envId)
		re := regexp.MustCompile(pattern)
		if !re.MatchString(entry.Name()) {
			continue
		}

		runDir := filepath.Join(runDirsPath, entry.Name())
		locked, err := isRunDirLocked(runDir)
		if err != nil {
			return false, err
		}
		if locked {
			return true, nil
		}
	}
	return false, nil
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

// initEnvironmentTmpDir checks if a tmp directory for the current
// environment already exists and has correct owner and
// permissions. If this is not the case, it creates a new such
// directory in tmp.
//
// Subdirs are created in /tmp/drop-username[-suffix]/ parent dir,
// which is readable only by the current user. This is to avoid
// polluting /tmp with a separate dir for each drop environment and to
// avoid exposing environment ids via /tmp.
//
// In order to keep track of already existing tmp sub-directory, a
// link in the env directory is created that points to it.
//
// The function returns a path to the tmp subdirectory.
func initEnvironmentTmpDir(envId string, paths *Paths) (string, error) {
	tmpSymlink := filepath.Join(paths.Env, "tmp")

	if target, err := os.Readlink(tmpSymlink); err == nil {
		// No error - symlink exists
		if tmpDirExistsWithRightPerms(target) {
			return target, nil
		}
		// Target directory is missing or invalid, remove the symlink, and
		// create a new tmp sub dir.
		os.Remove(tmpSymlink)
	}
	userName := filepath.Base(paths.HostHome)
	parentPath, err := createTmpParentDir(userName)
	if err != nil {
		return "", fmt.Errorf("failed to create parent temporary directory: %v", err)
	}

	tmpSubDir, err := os.MkdirTemp(parentPath, envId+"-")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %v", err)
	}

	// Create symbolic link to the tmp directory
	if err := os.Symlink(tmpSubDir, tmpSymlink); err != nil {
		return "", fmt.Errorf("failed to create symlink: %v", err)
	}
	return tmpSubDir, nil
}

// createTmpParentDir tries to create /tmp/drop-{USERNAME} dir. If
// such dir already exists, the function checks if it is owned by the
// current user and has permissions 0700. If yes, this directory path
// is returned. Otherwise, a directory
// /tmp/drop-{USERNAME}-{random-suffix} is created and returned.
func createTmpParentDir(userName string) (string, error) {
	parentName := fmt.Sprintf("drop-%s", userName)

	// In most cases the parent dir without a random suffix will be
	// created and then re-used. The suffixes are only added as a
	// fallback for cases where some other user created a tmp dir with
	// name that drop is using.
	parentPath := filepath.Join(os.TempDir(), parentName)
	err := os.Mkdir(parentPath, 0700)
	if err == nil || (os.IsExist(err) && tmpDirExistsWithRightPerms(parentPath)) {
		return parentPath, nil
	}
	return os.MkdirTemp("", parentName+"-")
}

func tmpDirExistsWithRightPerms(path string) bool {
	// Check if directory exists, is owned by current user, and has 700 permissions
	if stat, err := os.Stat(path); err == nil && stat.IsDir() {
		if sysStats, ok := stat.Sys().(*syscall.Stat_t); ok {
			// This works also when Drop is run with -r, because linux
			// correctly maps files owned by the user to have owner uuid of
			// 0 in the namespace.
			currentUID := os.Getuid()
			if int(sysStats.Uid) == currentUID && stat.Mode().Perm() == 0700 {
				return true
			}
		}
	}
	return false
}

func LsEnvs(dropHome string) ([]string, error) {
	envsPath := filepath.Join(dropHome, "envs")
	entries, err := os.ReadDir(envsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var envs []string
	for _, entry := range entries {
		if entry.IsDir() {
			envs = append(envs, entry.Name())
		}
	}
	return envs, nil
}

func RmEnv(dropHome string, envId string) error {
	if !IsEnvIdValid(envId) {
		return fmt.Errorf("invalid environment ID")
	}

	envPath := filepath.Join(dropHome, "envs", envId)

	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return fmt.Errorf("environment does not exist")
	}

	removeOrphanedRunDirs(runDirsPath(dropHome))

	// Check if there are any running Drop instances using this environment
	running, err := hasRunningDropInstances(runDirsPath(dropHome), envId)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("environment is used by running drop instances")
	}

	// Clean up the tmp directory (best effort)
	tmpSymlink := filepath.Join(envPath, "tmp")
	if target, err := os.Readlink(tmpSymlink); err == nil {
		// No error
		os.RemoveAll(target)
	}

	if err := os.RemoveAll(envPath); err != nil {
		return err
	}

	return nil
}
