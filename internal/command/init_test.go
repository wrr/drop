package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/jailfs"
	"github.com/wrr/drop/internal/osutil"
)

func TestInitEnv(t *testing.T) {
	t.Run("creates env dir and both config files", func(t *testing.T) {
		homeDir, dropHome, cwd := setupTestInit(t)

		if err := InitEnv("myenv", false, homeDir, dropHome); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertExists(t, jailfs.EnvPath(dropHome, "myenv"))
		assertExists(t, jailfs.BaseConfigPath(homeDir))
		envConfig := jailfs.EnvConfigPath(homeDir, "myenv")
		assertExists(t, envConfig)

		// cwd is a subdirectory of homeDir, so env config should contain
		// cwd mount.
		cfg := readConfig(t, envConfig, homeDir)
		if !hasMountSource(cfg.Mounts, cwd) {
			t.Fatalf("env config mounts don't contain cwd %q", cwd)
		}
	})

	t.Run("noCwd omits cwd from env config", func(t *testing.T) {
		homeDir, dropHome, cwd := setupTestInit(t)

		if err := InitEnv("myenv", true, homeDir, dropHome); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		cfg := readConfig(t, jailfs.EnvConfigPath(homeDir, "myenv"), homeDir)
		if hasMountSource(cfg.Mounts, cwd) {
			t.Fatal("env config should not contain cwd when noCwd is true")
		}
	})

	t.Run("cwd is a homeDir omits cwd from env config", func(t *testing.T) {
		_, dropHome, cwd := setupTestInit(t)

		// Set homeDir to cwd, in such case cwd should not be mount to
		// avoid exposing the whole home dir.
		homeDir := cwd
		if err := InitEnv("myenv", false, homeDir, dropHome); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		cfg := readConfig(t, jailfs.EnvConfigPath(cwd, "myenv"), cwd)
		if hasMountSource(cfg.Mounts, cwd) {
			t.Fatal("env config should not contain cwd mount when cwd is homeDir")
		}
	})

	t.Run("skips writing base config if it already exists", func(t *testing.T) {
		homeDir, dropHome, _ := setupTestInit(t)

		baseConfigPath := jailfs.BaseConfigPath(homeDir)
		os.MkdirAll(filepath.Dir(baseConfigPath), 0700)
		os.WriteFile(baseConfigPath, []byte("existing"), 0644)

		if err := InitEnv("myenv", false, homeDir, dropHome); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, _ := os.ReadFile(baseConfigPath)
		if string(content) != "existing" {
			t.Fatal("base config was overwritten")
		}
	})

	t.Run("skips writing env config if it already exists", func(t *testing.T) {
		homeDir, dropHome, _ := setupTestInit(t)

		envConfigPath := jailfs.EnvConfigPath(homeDir, "myenv")
		os.MkdirAll(filepath.Dir(envConfigPath), 0700)
		os.WriteFile(envConfigPath, []byte("existing"), 0644)

		if err := InitEnv("myenv", false, homeDir, dropHome); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		content, _ := os.ReadFile(envConfigPath)
		if string(content) != "existing" {
			t.Fatal("env config was overwritten")
		}
	})

	t.Run("invalid env ID returns error", func(t *testing.T) {
		homeDir, dropHome, _ := setupTestInit(t)

		err := InitEnv("-invalid", false, homeDir, dropHome)
		if err == nil || !strings.Contains(err.Error(), "invalid environment ID: -invalid") {
			t.Fatalf("expected 'invalid environment ID' error, got: %v", err)
		}
	})

	t.Run("Existing env returns error", func(t *testing.T) {
		homeDir, dropHome, _ := setupTestInit(t)

		if err := InitEnv("myenv", false, homeDir, dropHome); err != nil {
			t.Fatalf("first init failed: %v", err)
		}
		err := InitEnv("myenv", false, homeDir, dropHome)
		if err == nil || !strings.Contains(err.Error(), "myenv already exists") {
			t.Fatalf("expected 'already exists' error, got: %v", err)
		}
	})
}

// setupTestInit creates temp dirs and sets env vars so InitEnv uses
// isolated paths. Returns homeDir, dropHome and current working
// directory.
func setupTestInit(t *testing.T) (string, string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	cwd := filepath.Join(homeDir, "project")
	dropHome := filepath.Join(tmpDir, "drop-home")
	for _, dir := range []string{homeDir, dropHome, cwd} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("mkdirall failed %v: %v", dir, err)
		}
	}
	t.Chdir(cwd)

	// Set DROP_HOME so configs and env dir are created in tmpDir
	t.Setenv("DROP_HOME", dropHome)
	return homeDir, dropHome, cwd
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if !osutil.Exists(path) {
		t.Fatalf("expected %q to exist", path)
	}
}

func readConfig(t *testing.T, path, homeDir string) *config.Config {
	t.Helper()
	cfg, err := config.Read(path, homeDir)
	if err != nil {
		t.Fatalf("failed to read config %s: %v", path, err)
	}
	return cfg
}

func hasMountSource(mounts []config.Mount, source string) bool {
	for _, m := range mounts {
		if m.Source == source {
			return true
		}
	}
	return false
}
