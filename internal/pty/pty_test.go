package pty

import (
	"testing"
)

func TestNewPty(t *testing.T) {
	parent, child, err := NewPty()
	if err != nil {
		t.Fatalf("NewPty() failed: %v", err)
	}
	defer parent.Close()
	defer child.Close()

	// Test parent -> child communication
	testMsg1 := []byte("Hello:\nparent\n")
	n, err := parent.Write(testMsg1)
	if err != nil {
		t.Fatalf("Failed to write to parent: %v", err)
	}
	if n != len(testMsg1) {
		t.Errorf("Parent write: expected %d bytes, wrote %d bytes", len(testMsg1), n)
	}

	buf := make([]byte, 256)
	n, err = child.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read from child: %v", err)
	}
	if string(buf[:n]) != string(testMsg1) {
		t.Errorf("Parent->Child: expected %q, got %q", testMsg1, buf[:n])
	}

	// Test child -> parent communication
	testMsg2 := []byte("Hello:\nchild\n")
	n, err = child.Write(testMsg2)
	if err != nil {
		t.Fatalf("Failed to write to child: %v", err)
	}
	if n != len(testMsg2) {
		t.Errorf("Child write: expected %d bytes, wrote %d bytes", len(testMsg2), n)
	}

	n, err = parent.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read from parent: %v", err)
	}
	if string(buf[:n]) != string(testMsg2) {
		t.Errorf("Child->Parent: expected %q, got %q", testMsg2, buf[:n])
	}
}
