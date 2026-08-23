// Copyright 2026 Jan Wrobel <jan@mixedbit.org>
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

package ipc

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendRecvPty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pty.sock")

	receiver, err := NewPtyReceiver(path)
	if err != nil {
		t.Fatalf("NewPtyReceiver: %v", err)
	}
	defer receiver.Close()

	const content = "hello"
	src, err := os.CreateTemp(t.TempDir(), "src")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := src.WriteString(content); err != nil {
		t.Fatal(err)
	}

	sendErr := make(chan error, 1)
	go func() {
		sender, err := NewPtySender(path)
		if err != nil {
			sendErr <- err
			return
		}
		defer sender.Close()
		sendErr <- sender.SendPty(src)
	}()

	received, err := receiver.RecvPty()
	if err != nil {
		t.Fatalf("RecvPty: %v", err)
	}
	defer received.Close()
	if err := <-sendErr; err != nil {
		t.Fatalf("SendPty: %v", err)
	}

	// The received descriptor refers to the same open file, so reading
	// from it (after rewinding) yields the content written via src.
	if _, err := received.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(received)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("received descriptor content = %q, want %q", got, content)
	}
}

// TestRecvPtyEOF verifies RecvPty reports io.EOF when the sender connects
// but closes without sending a descriptor (e.g. the child died before the
// handoff), so callers can distinguish it from a real error.
func TestRecvPtyEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pty.sock")

	receiver, err := NewPtyReceiver(path)
	if err != nil {
		t.Fatalf("NewPtyReceiver: %v", err)
	}
	defer receiver.Close()

	go func() {
		// Connect, then close without sending anything.
		sender, err := NewPtySender(path)
		if err != nil {
			return
		}
		sender.Close()
	}()

	if _, err := receiver.RecvPty(); !errors.Is(err, io.EOF) {
		t.Errorf("RecvPty err = %v, want io.EOF", err)
	}
}

func TestPtySocketPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pty.sock")

	receiver, err := NewPtyReceiver(path)
	if err != nil {
		t.Fatalf("NewPtyReceiver: %v", err)
	}
	defer receiver.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("socket permissions = %#o, want %#o", got, 0o700)
	}
}

func TestPtySocketPathTooLong(t *testing.T) {
	receiver, err := NewPtyReceiver("/tmp/" + strings.Repeat("x", 103))
	if err == nil {
		receiver.Close()
		t.Fatalf("NewPtyReceiver did not fail with long path")
	}
	if !strings.HasPrefix(err.Error(), "unix socket path longer than 107") {
		t.Errorf("unexpected error: %v", err)
	}
}
