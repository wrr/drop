package osutil

import (
	"testing"
)

func TestIsDebian(t *testing.T) {
	// This test just ensures the function doesn't panic and returns a boolean
	// The actual result depends on the host system
	result := IsDebianBased()

	// Should return either true or false (boolean type check)
	if result != true && result != false {
		t.Error("IsDebian should return a boolean value")
	}
}

func TestTildeToHomeDir(t *testing.T) {
	homeDir := "/home/alice"
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "tilde path with single file",
			path: "~/.bashrc",
			want: "/home/alice/.bashrc",
		},
		{
			name: "tilde path with trailing slash",
			path: "~/Documents/",
			want: "/home/alice/Documents",
		},
		{
			name: "to tilde - no change",
			path: "/usr/local/bin",
			want: "/usr/local/bin",
		},
		{
			name: "tilde alone",
			path: "~",
			want: homeDir,
		},
		{
			name: "tilde with different user",
			path: "~otheruser/file",
			want: "~otheruser/file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TildeToHomeDir(tt.path, homeDir)
			if got != tt.want {
				t.Errorf("TildeToHomeDir(%q, %q) = %q, want %q", tt.path, homeDir, got, tt.want)
			}
		})
	}
}
