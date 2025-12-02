package jailfs

import (
	"os"
	"strings"
	"testing"
)

func TestIsEnvIdValid(t *testing.T) {
	tests := []struct {
		name  string
		envId string
		want  bool
	}{
		{
			name:  "simple alphanumeric",
			envId: "project123",
			want:  true,
		},
		{
			name:  "with dash",
			envId: "project-foo",
			want:  true,
		},
		{
			name:  "with underscore",
			envId: "project_foo",
			want:  true,
		},
		{
			name:  "with dot",
			envId: "project.foo",
			want:  true,
		},
		{
			name:  "mixed valid characters",
			envId: "Project_123-test.v2",
			want:  true,
		},
		{
			name:  "single character",
			envId: "a",
			want:  true,
		},
		{
			name:  "starts with dash",
			envId: "-project",
			want:  false,
		},
		{
			name:  "starts with dot",
			envId: ".project",
			want:  false,
		},
		{
			name:  "with slash",
			envId: "project/foo",
			want:  false,
		},
		{
			name:  "with space",
			envId: "project foo",
			want:  false,
		},
		{
			name:  "with special char",
			envId: "project@foo",
			want:  false,
		},
		{
			name:  "empty",
			envId: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsEnvIdValid(tt.envId)
			if got != tt.want {
				t.Errorf("IsEnvIdValid(%q) = %v, want %v", tt.envId, got, tt.want)
			}
		})
	}
}

func TestPathToEnvId(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		// Root directory special case
		{
			name: "root directory",
			path: "/",
			want: "root",
		},
		{
			name: "single level",
			path: "/tmp",
			want: "tmp",
		},
		{
			name: "nested path",
			path: "/home/alice/projects/foo",
			want: "home-alice-projects-foo",
		},
		{
			name: "path with dots",
			path: "/home/alice/project.v1.2",
			want: "home-alice-project.v1.2",
		},
		{
			name: "top dir with dot",
			path: "/.hidden",
			want: "hidden",
		},
		{
			name: "path with spaces",
			path: "/home/alice/my project",
			want: "home-alice-my_project",
		},
		{
			name: "path with special chars",
			path: "/home/alice/project@work",
			want: "home-alice-project_work",
		},
		{
			name: "path with parentheses",
			path: "/home/alice/project(v2)",
			want: "home-alice-project_v2_",
		},
		{
			name: "path with underscores",
			path: "/home/alice/my_project",
			want: "home-alice-my_project",
		},
		{
			name: "path with numbers",
			path: "/home/alice/project123",
			want: "home-alice-project123",
		},
		// Edge cases, should not be triggered with input path being CWD.
		{
			name: "empty string",
			path: "",
			want: "root",
		},
		{
			name: "path with only slashes",
			path: "///",
			want: "root",
		},
		{
			name: "path with only dots and slashes",
			path: "/./.././/",
			want: "root",
		},
		{
			name: "only dot",
			path: ".",
			want: "root",
		},
		{
			name: "relative path",
			path: "relative/path",
			want: "relative-path",
		},
		{
			name: "path ending with slash",
			path: "/home/alice/",
			want: "home-alice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathToEnvId(tt.path)
			if got != tt.want {
				t.Errorf("pathToEnvId(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestDefaultConfigPath(t *testing.T) {
	dropHome := "/home/alice/.config/drop"
	result := DefaultConfigPath(dropHome)
	expected := "/home/alice/.config/drop/config.toml"
	if result != expected {
		t.Errorf("DefaultConfigPath(%q) = %q", dropHome, result)
	}
}

func TestDropHome(t *testing.T) {
	homeDir := "/home/alice"

	tests := []struct {
		name      string
		dropHome  string
		want      string
		wantError string
	}{
		{
			name:     "env not set",
			dropHome: "",
			want:     "/home/alice/.drop",
		},
		{
			name:     "absolute path",
			dropHome: "/var/drop-data",
			want:     "/var/drop-data",
		},
		{
			name:     "tilde path",
			dropHome: "~/.my-drop",
			want:     "/home/alice/.my-drop",
		},
		{
			name:      "relative path",
			dropHome:  "relative/path",
			wantError: "path must start with / or ~/",
		},
		{
			name:      "not normalized",
			dropHome:  "/var/../etc/drop",
			wantError: "path is not normalized",
		},
		{
			name:      "whole root",
			dropHome:  "/",
			wantError: "path cannot point to the whole root directory",
		},
		{
			name:      "whole home",
			dropHome:  "~/",
			wantError: "path cannot point to the whole home directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dropHome != "" {
				os.Setenv("DROP_HOME", tt.dropHome)
				defer os.Unsetenv("DROP_HOME")
			} else {
				os.Unsetenv("DROP_HOME")
			}

			got, err := DropHome(homeDir)
			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantError)
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("expected error containing %q, got %q", tt.wantError, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("DropHome(%q) = %q, want %q", homeDir, got, tt.want)
			}
		})
	}
}
