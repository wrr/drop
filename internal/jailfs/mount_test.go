package jailfs

import "testing"

func TestIsSubDir(t *testing.T) {
	tests := []struct {
		name           string
		parent         string
		child          string
		isSubDir       bool
		isSubDirOrSame bool
	}{
		{
			name:           "direct parent",
			parent:         "/home/alice",
			child:          "/home/alice/documents",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "nested parent",
			parent:         "/home",
			child:          "/home/alice/documents/file.txt",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "not parent - sibling",
			parent:         "/home/alice",
			child:          "/home/other",
			isSubDir:       false,
			isSubDirOrSame: false,
		},
		{
			name:           "not parent - completely different",
			parent:         "/home/alice",
			child:          "/var/log",
			isSubDir:       false,
			isSubDirOrSame: false,
		},
		{
			name:           "same directory",
			parent:         "/home/alice",
			child:          "/home/alice",
			isSubDir:       false,
			isSubDirOrSame: true,
		},
		{
			name:           "parent with trailing slash",
			parent:         "/home/alice/",
			child:          "/home/alice/documents",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "child with relative components",
			parent:         "/home/alice",
			child:          "/home/alice/..",
			isSubDir:       false,
			isSubDirOrSame: false,
		},
		{
			name:           "child with relative components 2",
			parent:         "/home/alice",
			child:          "/home/alice/../../home/alice",
			isSubDir:       false,
			isSubDirOrSame: true,
		},
		{
			name:           "parent with relative components",
			parent:         "/home/./alice/..",
			child:          "/home/documents",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "root as parent",
			parent:         "/",
			child:          "/home/alice",
			isSubDir:       true,
			isSubDirOrSame: true,
		},
		{
			name:           "root as parent and child",
			parent:         "/",
			child:          "/",
			isSubDir:       false,
			isSubDirOrSame: true,
		},
		{
			name:           "substring but not parent",
			parent:         "/home/use",
			child:          "/home/alice",
			isSubDir:       false,
			isSubDirOrSame: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSubDir(tt.parent, tt.child)
			if result != tt.isSubDir {
				t.Errorf("isSubDir(%q, %q) = %v, want %v", tt.parent, tt.child, result, tt.isSubDir)
			}
			result = isSubDirOrSame(tt.parent, tt.child)
			if result != tt.isSubDirOrSame {
				t.Errorf("isSubDirOrSame(%q, %q) = %v, want %v", tt.parent, tt.child, result, tt.isSubDirOrSame)
			}
		})
	}
}
