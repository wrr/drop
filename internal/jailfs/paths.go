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
	// Run holds temporary files and dirs for the current jail instance.
	// It can be safely removed once the jailed process terminates.
	// Run is located within Paths.DropHome.
	Run string
	// Like Run dir, but located in /tmp/drop-{username}/run/.
	//
	// Stores pasta runtime files (pid and log). This ensures no
	// extra configuration is needed on systems where SELinux
	// or AppArmor configs allow pasta to write files only to /tmp.
	//
	// Stores PtySocket. Has a limited total path length (unlike
	// Paths.Run) to mitigate problems with unix socket path length
	// limit (although these can still be hit with custom TMPDIR or very
	// long username).
	RunInTmp string
	// PtySocket is a path to a named socket that the child uses to send
	// pty to the parent.
	PtySocket string
	// EmptyDir is an empty directory used to hide directories in the jail.
	EmptyDir string
	// EmptyFile is an empty file used to hide files in the jail.
	EmptyFile string
}

// CwdCandidates returns the candidates for the working directory to
// use for the sandboxed process, ordered by preference: the directory
// Drop was started from, the home dir and "/". The first candidate
// dir that exists in the sandbox should be used as the sandboxed
// process CWD.
func (p *Paths) CwdCandidates() []string {
	return []string{p.Cwd, p.HostHome, "/"}
}

// NewPaths creates Paths object with the relevant paths for the
// current environment and creates missing dir and files.
//
// On success, the second return value is a cleanup function that
// should be called when Drop terminates.
func NewPaths(hostHome string, envId string) (*Paths, func(), error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	dropHome, err := DropHome(hostHome)
	if err != nil {
		return nil, nil, err
	}

	userName := filepath.Base(hostHome)
	tmpRoot, err := createTmpRootDir(userName)
	if err != nil {
		return nil, nil, err
	}

	env := EnvPath(dropHome, envId)
	internal := filepath.Join(dropHome, "internal")
	runDir, runInTmp, cleanRunDir, err := newRunDir(dropHome, envId, tmpRoot)
	if err != nil {
		return nil, nil, err
	}
	success := false
	defer func() {
		if !success {
			cleanRunDir()
		}
	}()

	paths := Paths{
		Cwd:       cwd,
		DropHome:  dropHome,
		Env:       env,
		FsRoot:    filepath.Join(runDir, "root"),
		HostHome:  hostHome,
		Home:      filepath.Join(env, "home"),
		Etc:       filepath.Join(env, "etc"),
		Var:       filepath.Join(env, "var"),
		Run:       runDir,
		RunInTmp:  runInTmp,
		PtySocket: filepath.Join(runInTmp, "pty.sock"),
		EmptyDir:  filepath.Join(internal, "emptyd"),
		EmptyFile: filepath.Join(internal, "empty"),
	}

	toMkdir := []string{paths.FsRoot, paths.Home, paths.Etc, paths.Var}
	for _, dir := range toMkdir {
		if err := osutil.MkdirAll(dir); err != nil {
			return nil, nil, err
		}
	}

	if err := ensureDirWithNoPerms(paths.EmptyDir); err != nil {
		return nil, nil, err
	}
	if err := ensureEmptyFile(paths.EmptyFile); err != nil {
		return nil, nil, err
	}

	tmp, err := initEnvTmpDir(envId, tmpRoot, paths.Env)
	if err != nil {
		return nil, nil, err
	}
	paths.Tmp = tmp
	success = true
	return &paths, cleanRunDir, nil
}

func ConfigDir(homeDir string) string {
	if path := os.Getenv("DROP_HOME"); path != "" {
		return filepath.Join(path, "config")
	}
	parent := os.Getenv("XDG_CONFIG_HOME")
	if parent == "" {
		parent = filepath.Join(homeDir, ".config")
	}
	return filepath.Join(parent, "drop")
}

// BaseConfigPath returns path to a base Drop config file which
// environment specific config files extend by default. If DROP_HOME
// is set, DROP_HOME/config/base.toml is returned, otherwise XDG
// specification is followed: the base config is in (XDG_CONFIG_HOME
// or "~/.config")/drop/base.toml
func BaseConfigPath(homeDir string) string {
	parent := ConfigDir(homeDir)
	return filepath.Join(parent, "base.toml")
}

func EnvPath(dropHome, envId string) string {
	return filepath.Join(dropHome, "envs", envId)
}

func EnvConfigPath(homeDir, envId string) string {
	parent := ConfigDir(homeDir)
	return filepath.Join(parent, envId+".toml")
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
	// 'base' is not allowed as environment id, because base.toml is a
	// shared config file that shouldn't be overwritten by environment
	// specific config.
	if envId == "base" {
		return false
	}
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
		return "", fmt.Errorf("get current directory: %v", err)
	}
	return pathToEnvId(cwd), nil
}

func runDirsPath(dropHome string) string {
	return filepath.Join(dropHome, "internal", "run")
}

// envIdToPrefix constructs a non-unique directory name prefix based
// on an envId. The prefix has at most maxLen characters and, like a
// valid envId, never starts with '-' or '.'. Because long env ids are
// commonly a result of long directory trees from which default env
// ids are constructed, the function takes last elements of such ids,
// as these contain the most informative directory names. If possible
// the function tries to select only complete entries.
//
// envId passed to this function must be valid (IsEnvIdValid(envId) == true).
//
// Some examples:
//
// home-alice-projects-shortname -> home-alice-projects-shortname
// home-alice-projects-superlongprojectname01234567 -> superlongprojectname01234567
// home-alice-projects-superextralongprojectname01234567 -> superextralongprojectname0123456
// home-alice-projects-shorterprojectnamebar -> projects-shorterprojectnamebar
// home-alice-projects-superlongprojectname01234567-subproject -> subproject
// home-alice-projects-.longhiddendirectoryname1234 -> longhiddendirectoryname1234
func envIdToPrefix(envId string) string {
	const maxLen = 32
	if len(envId) <= maxLen {
		return envId
	}
	parts := strings.Split(envId, "-")
	result := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		p := parts[i]
		total := len(p) + len(result) + 1
		if total > maxLen {
			break
		}
		result = p + "-" + result
	}
	// Entries separated by several '-' and entries that start with '.'
	// leave such characters at the front of the result.
	result = strings.TrimLeft(result, "-.")
	if len(result) == 0 {
		// Trimming left nothing, as fallback use the beginning of envId,
		// which starts with an allowed character.
		result = envId
	}
	if len(result) > maxLen {
		result = result[0:maxLen]
	}
	return result
}

const runLockFname string = "lock"
const runInTmpSymlinkName string = "run-in-tmp"

// newRunDir creates Paths.Run and Paths.RunInTmp directories to store
// this jail instance runtime files and dirs. The two directories can
// be removed when this jail instance terminates.
//
// The main runDir is located below dropHome. It is used to store, for
// example, the assembled root file system for this drop instance. We
// don't use XDG_RUNTIME_DIR, because it is commonly tmpfs and
// overlayfs mount points cannot be placed on it.
//
// The tmpRunDir is located below tmpRoot. Because it is expected to be
// on tmpfs, it cannot keep root file system and replace runDir.
//
// The total path length of tmpRunDir is limited, to prevent problems
// with Unix domain socket paths length limits.
func newRunDir(dropHome, envId, tmpRoot string) (string, string, func(), error) {
	parent := runDirsPath(dropHome)
	runInTmpParent := filepath.Join(tmpRoot, "run")
	toMkdir := []string{parent, runInTmpParent}
	for _, dir := range toMkdir {
		if err := osutil.MkdirAll(dir); err != nil {
			return "", "", nil, err
		}
	}

	// We use full envId, not shorter version, as we do for runInTmp
	// below. This is because there is no path length limit for things
	// stored in runDir, and hasRunningDropInstances() needs run dir to
	// use full envId.
	runDir, err := os.MkdirTemp(parent, envId+"-")
	if err != nil {
		return "", "", nil, fmt.Errorf("create run sub-directory in drop home: %v", err)
	}

	lockFile, err := lockRunDir(runDir)
	if err != nil {
		return "", "", nil, err
	}

	// Shorten long env ids to make runInTmp paths shorter.
	runInTmp, err := os.MkdirTemp(runInTmpParent, envIdToPrefix(envId)+"-")
	if err != nil {
		return "", "", nil, fmt.Errorf("create run sub-directory in tmp: %v", err)
	}

	// Link from the runDir to runDirInTmp, so it can be easily located
	// and cleaned.
	runInTmpSymlink := filepath.Join(runDir, runInTmpSymlinkName)
	if err := os.Symlink(runInTmp, runInTmpSymlink); err != nil {
		os.Remove(runInTmp)
		return "", "", nil, fmt.Errorf("create symlink: %v", err)
	}

	// cleanRunDir removes runtime files no longer needed when Drop
	// terminates.
	cleanRunDir := func() {
		// Releases the lock (not crucial, because proccess termination
		// also does it).
		lockFile.Close()
		// Remove the current instance run dir
		if err := removeRunDir(runDir); err != nil {
			log.Info("failed to clean run dir: %v", err)
		}
		if err := removeOrphanedRunDirs(filepath.Dir(runDir)); err != nil {
			log.Info("failed to remove orphaned run dirs: %v", err)
		}
	}
	return runDir, runInTmp, cleanRunDir, nil
}

func removeRunDir(runDir string) error {
	runInTmpSymlink := filepath.Join(runDir, runInTmpSymlinkName)
	if runInTmp, err := os.Readlink(runInTmpSymlink); err == nil {
		// No error - remove also run directory in tmp (best-effort).
		os.RemoveAll(runInTmp)
	}

	return os.RemoveAll(runDir)
}

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
			if err := removeRunDir(runDir); err != nil {
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
		return nil, fmt.Errorf("create a file: %v", err)
	}
	// Do not close the file, as this releases the lock. The lock should
	// be released when the process terminates.

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return nil, fmt.Errorf("lock a file %v: %v", lockPath, err)
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
		pattern := fmt.Sprintf(`^%s-\d+$`, regexp.QuoteMeta(envId))
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

	return osutil.CreateEmptyFile(path, 0000)
}

// initEnvTmpDir checks if a tmp directory for the current
// environment already exists and has correct owner and
// permissions. If this is not the case, it creates a new such
// directory in tmp.
//
// Subdirs are created in /tmp/drop-username[-suffix]/tmp/,
// which is readable only by the current user. This is to avoid
// polluting /tmp with a separate dir for each drop environment and to
// avoid exposing environment ids via /tmp.
//
// In order to keep track of already existing tmp sub-directory, a
// link in the env directory is created that points to it.
//
// The function returns a path to the tmp subdirectory.
func initEnvTmpDir(envId, tmpRootDir, envDir string) (string, error) {
	tmpSymlink := filepath.Join(envDir, "tmp")

	if target, err := os.Readlink(tmpSymlink); err == nil {
		// No error - symlink exists
		if tmpDirExistsWithRightPerms(target) {
			return target, nil
		}
		// Target directory is missing or invalid, remove the symlink, and
		// create a new tmp sub dir.
		os.Remove(tmpSymlink)
	}
	tmpEnvsDir := filepath.Join(tmpRootDir, "tmp")
	if err := osutil.MkdirAll(tmpEnvsDir); err != nil {
		return "", err
	}
	tmpSubDir, err := os.MkdirTemp(tmpEnvsDir, envId+"-")
	if err != nil {
		return "", fmt.Errorf("create temporary directory: %v", err)
	}

	// Create symbolic link to the tmp directory
	if err := os.Symlink(tmpSubDir, tmpSymlink); err != nil {
		return "", fmt.Errorf("create symlink: %v", err)
	}
	return tmpSubDir, nil
}

// createTmpRootDir tries to create drop-{USERNAME} dir in tmp. If
// such dir was successfully created or already exists with right
// owner and permissions, the directory path is returned. Otherwise,
// the function repeats the checks for directories with names
// drop-{USERNAME}-{numerical suffix 0 to 9}. If all these
// directories already exist and do not have the right permissions,
// the function returns an error.
func createTmpRootDir(userName string) (string, error) {
	dirName := fmt.Sprintf("drop-%s", userName)
	tmpDir := os.TempDir()
	// In most cases the parent dir without a suffix is created and then
	// re-used. The suffix is only added as a fallback for cases where
	// some other user created a tmp dir with name that drop is using.
	var err error
	path := filepath.Join(tmpDir, dirName)
	defaultPath := path
	for suffix := 0; suffix <= 10; suffix++ {
		err = os.Mkdir(path, 0700)
		if err == nil || (os.IsExist(err) && tmpDirExistsWithRightPerms(path)) {
			return path, nil
		}
		path = filepath.Join(tmpDir, fmt.Sprintf("%s-%d", dirName, suffix))
	}
	hdr := fmt.Sprintf("create root tmp directory %s(-suffix):", defaultPath)
	if os.IsExist(err) {
		return "", fmt.Errorf("%s dirs already exist but without the right owner or permissions", hdr)
	}
	return "", fmt.Errorf("%s %v", hdr, err)
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

func CreateEnvDir(dropHome string, envId string) error {
	if !IsEnvIdValid(envId) {
		return fmt.Errorf("invalid environment ID: %s", envId)
	}
	envPath := EnvPath(dropHome, envId)

	_, err := os.Stat(envPath)
	if err == nil {
		return fmt.Errorf("environment %s already exists", envId)
	}
	if !os.IsNotExist(err) {
		return err
	}
	return osutil.MkdirAll(envPath)
}

func RmEnv(homeDir, dropHome string, envId string) error {
	if !IsEnvIdValid(envId) {
		return fmt.Errorf("invalid environment ID: %s", envId)
	}

	envPath := EnvPath(dropHome, envId)

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

	envConfigPath := EnvConfigPath(homeDir, envId)
	if osutil.CanStat(envConfigPath) {
		if err := os.Remove(envConfigPath); err != nil {
			return fmt.Errorf("remove environment config %v: %v", envConfigPath, err)
		}
	}

	return nil
}
