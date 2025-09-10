package jailfs

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
)

type Paths struct {
	Cwd        string
	Config     string
	Base       string
	FsRoot     string
	HostHome   string
	Home       string
	Etc        string
	TmpSrc     string
	TmpDst     string
	EmptyDir   string
	EmptyFile  string
	ResolvConf string
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

func writeResolvConf(path string) error {
	content := "nameserver 10.0.2.3"
	return os.WriteFile(path, []byte(content), 0600)
}

func tmpDirName(jailId string) string {
	return "dirjail-" + jailId + "-"
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

func NewPaths(jailId string) (*Paths, error) {
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
	config := filepath.Join(dotdir, "config")
	internal := filepath.Join(dotdir, "internal")
	fsRoot := filepath.Join(base, "root")
	etc := filepath.Join(base, "etc")
	paths := Paths{
		Cwd:        cwd,
		Config:     config,
		Base:       base,
		FsRoot:     fsRoot,
		HostHome:   hostHome,
		Home:       filepath.Join(base, "home"),
		Etc:        etc,
		TmpDst:     filepath.Join(fsRoot, os.TempDir()),
		EmptyDir:   filepath.Join(internal, "emptyd"),
		EmptyFile:  filepath.Join(internal, "empty"),
		ResolvConf: filepath.Join(etc, "resolv.conf"),
	}

	if err := os.MkdirAll(paths.Home, 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %v", paths.Home, err)
	}

	if err := os.MkdirAll(paths.Etc, 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %v", paths.Etc, err)
	}
	if err := os.MkdirAll(internal, 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %v", internal, err)
	}

	if err := ensureDirWithNoPerms(paths.EmptyDir); err != nil {
		return nil, err
	}
	if err := ensureEmptyFile(paths.EmptyFile); err != nil {
		return nil, err
	}
	if err := writeResolvConf(paths.ResolvConf); err != nil {
		return nil, fmt.Errorf("failed to create resolv.conf file: %v", err)
	}

	tmpSrc, err := initTmpSubDir(jailId, &paths)
	if err != nil {
		return nil, err
	}
	paths.TmpSrc = tmpSrc
	return &paths, nil
}
