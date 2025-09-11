package jailfs

import "testing"

func TestIsParentDir(t *testing.T) {
	tests := []struct {
		name     string
		parent   string
		child    string
		expected bool
	}{
		{
			name:     "direct parent",
			parent:   "/home/alice",
			child:    "/home/alice/documents",
			expected: true,
		},
		{
			name:     "nested parent",
			parent:   "/home",
			child:    "/home/alice/documents/file.txt",
			expected: true,
		},
		{
			name:     "not parent - sibling",
			parent:   "/home/alice",
			child:    "/home/other",
			expected: false,
		},
		{
			name:     "not parent - completely different",
			parent:   "/home/alice",
			child:    "/var/log",
			expected: false,
		},
		{
			name:     "same directory",
			parent:   "/home/alice",
			child:    "/home/alice",
			expected: true,
		},
		{
			name:     "parent with trailing slash",
			parent:   "/home/alice/",
			child:    "/home/alice/documents",
			expected: true,
		},
		{
			name:     "child with relative components",
			parent:   "/home/alice",
			child:    "/home/alice/..",
			expected: false,
		},
		{
			name:     "child with relative components 2",
			parent:   "/home/alice",
			child:    "/home/alice/../../home/alice",
			expected: true,
		},
		{
			name:     "parent with relative components",
			parent:   "/home/./alice/..",
			child:    "/home/documents",
			expected: true,
		},
		{
			name:     "root as parent",
			parent:   "/",
			child:    "/home/alice",
			expected: true,
		},
		{
			name:     "root as parent and child",
			parent:   "/",
			child:    "/",
			expected: true,
		},
		{
			name:     "substring but not parent",
			parent:   "/home/use",
			child:    "/home/alice",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isParentOrSame(tt.parent, tt.child)
			if result != tt.expected {
				t.Errorf("isParentDir(%q, %q) = %v, want %v", tt.parent, tt.child, result, tt.expected)
			}
		})
	}
}
