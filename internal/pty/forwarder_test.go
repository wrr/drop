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

package pty

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

// testForwarder is a forwarder connected to a real PTY, with pipes in
// place of the host terminal.
type testForwarder struct {
	fwd *forwarder
	// The sandboxed process side of the PTY.
	sandboxPty *os.File
	// Reads what the forwarder writes to the host terminal.
	hostOutputReader *os.File
	// Writes the user input. Nil without an input terminal.
	hostInputWriter *os.File
}

func newTestForwarder(t *testing.T, withInput bool) *testForwarder {
	t.Helper()
	parent, child, err := NewPty()
	if err != nil {
		t.Fatal(err)
	}
	makeRaw(t, child)

	outputReader, outputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var inputReader, inputWriter *os.File
	if withInput {
		inputReader, inputWriter, err = os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
	}

	fwd := newForwarder(parent, outputWriter, inputReader)
	t.Cleanup(func() {
		select {
		case <-fwd.stopped:
		default:
			fwd.stop()
		}
		// Ends forwardOutput with EIO.
		child.Close()
		if withInput {
			// Ends forwardInput with EOF.
			inputWriter.Close()
			inputReader.Close()
		}
		outputWriter.Close()
		outputReader.Close()
	})
	return &testForwarder{
		fwd:              fwd,
		sandboxPty:       child,
		hostOutputReader: outputReader,
		hostInputWriter:  inputWriter,
	}
}

// makeRaw switches the terminal to raw mode (no input echo and
// no changing of \n into \r\n). Fd() isn't used, because it switches
// the descriptor to blocking mode, in which read deadlines used by the
// tests don't work.
func makeRaw(t *testing.T, terminal *os.File) {
	t.Helper()
	conn, err := terminal.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var rawErr error
	err = conn.Control(func(fd uintptr) {
		_, rawErr = term.MakeRaw(int(fd))
	})
	if err != nil {
		t.Fatal(err)
	}
	if rawErr != nil {
		t.Fatal(rawErr)
	}
}

// readFull reads exactly n bytes from the file.
func readFull(t *testing.T, file *os.File, n int) string {
	t.Helper()
	// so a broken forwarder fails the test instead of hanging it.
	file.SetReadDeadline(time.Now().Add(2 * time.Second))
	got := make([]byte, n)
	read, err := io.ReadFull(file, got)
	if err != nil {
		t.Fatalf("read %q: %v", got[:read], err)
	}
	return string(got)
}

func write(t *testing.T, file *os.File, data string) {
	t.Helper()
	if _, err := file.WriteString(data); err != nil {
		t.Fatal(err)
	}
}

// noInputDiscard lets the input reach readKey right after pause.
func noInputDiscard(f *forwarder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discardUntil = time.Time{}
}

func TestForwardOutput(t *testing.T) {
	tf := newTestForwarder(t, false)
	s := "sandbox output\n"
	write(t, tf.sandboxPty, s)
	got := readFull(t, tf.hostOutputReader, len(s))
	if got != s {
		t.Errorf("Expected %q, got %q", s, got)
	}
}

func TestForwardInput(t *testing.T) {
	tf := newTestForwarder(t, true)
	s := "user input"
	write(t, tf.hostInputWriter, s)
	got := readFull(t, tf.sandboxPty, len(s))
	if got != s {
		t.Errorf("Expected %q, got %q", s, got)
	}
}

func TestPauseWithholdsOutputUntilResume(t *testing.T) {
	tf := newTestForwarder(t, true)
	if err := tf.fwd.pause(); err != nil {
		t.Fatal(err)
	}
	sandboxOut := "hello world"
	write(t, tf.sandboxPty, sandboxOut)

	// Drop prompt written while firewall is paused.
	promptOut := "firewall prompt"
	// TODO: change this when forwarder exposes API for writing.
	write(t, tf.fwd.hostOutput, promptOut)

	got := readFull(t, tf.hostOutputReader, len(promptOut))
	if got != promptOut {
		t.Errorf("Expected %q, got %q", promptOut, got)
	}

	tf.fwd.resume()
	// Only when forwarder is resumed the initial sandbox output is
	// written to the host terminal.
	got = readFull(t, tf.hostOutputReader, len(sandboxOut))
	if got != sandboxOut {
		t.Errorf("Expected %q, got %q", sandboxOut, got)
	}
}

func TestPauseRoutesInput(t *testing.T) {
	tf := newTestForwarder(t, true)
	write(t, tf.hostInputWriter, "a")
	if got := readFull(t, tf.sandboxPty, 1); got != "a" {
		t.Errorf("Expected %q, got %q", "a", got)
	}
	if err := tf.fwd.pause(); err != nil {
		t.Fatal(err)
	}
	noInputDiscard(tf.fwd)
	write(t, tf.hostInputWriter, "b")
	if key, err := tf.fwd.readKey(); err != nil || key != 'b' {
		t.Errorf("readKey = %q, %v, want b", key, err)
	}

	tf.fwd.resume()
	write(t, tf.hostInputWriter, "c")
	if got := readFull(t, tf.sandboxPty, 1); got != "c" {
		t.Errorf("Expected %q, got %q", "c", got)
	}
}

func TestRouteInput(t *testing.T) {
	tf := newTestForwarder(t, true)
	f := tf.fwd
	if !f.routeInput([]byte("a")) {
		t.Error("input not forwarded when not paused")
	}
	if err := f.pause(); err != nil {
		t.Fatal(err)
	}
	if f.routeInput([]byte("b")) {
		t.Error("input forwarded when paused")
	}
	if n := len(f.capturedKey); n != 0 {
		t.Errorf("%d bytes captured during the discard period", n)
	}

	noInputDiscard(f)
	// Up arrow, X10 mouse click at column 33, pastes.
	for _, input := range []string{"\x1b[A", "\x1b[M A!", "ad", "aab"} {
		if f.routeInput([]byte(input)) {
			t.Errorf("input %q forwarded while paused", input)
		}
	}
	if n := len(f.capturedKey); n != 0 {
		t.Errorf("%d keys captured from multi-byte input", n)
	}

	f.routeInput([]byte("aaa"))
	if n := len(f.capturedKey); n != 1 {
		t.Fatalf("captured %d keys from repeated presses, want 1", n)
	}
	if b, err := f.readKey(); err != nil || b != 'a' {
		t.Errorf("readKey = %q, %v, want a", b, err)
	}

	for range maxCapturedKeys {
		f.routeInput([]byte("c"))
	}
	if f.routeInput([]byte("d")) {
		t.Error("input forwarded while paused")
	}
	if n := len(f.capturedKey); n != maxCapturedKeys {
		t.Errorf("captured %d keys, want %d", n, maxCapturedKeys)
	}
	if b, err := f.readKey(); err != nil || b != 'c' {
		t.Errorf("readKey = %q, %v, want c", b, err)
	}

	f.resume()
	if !f.routeInput([]byte("e")) {
		t.Error("input not forwarded after resume")
	}
}

func TestPauseErrors(t *testing.T) {
	tf := newTestForwarder(t, false)
	if err := tf.fwd.pause(); err == nil || !strings.Contains(err.Error(), "not a terminal") {
		t.Errorf("expected an error without an input terminal, got %v", err)
	}
	tf = newTestForwarder(t, true)
	if err := tf.fwd.pause(); err != nil {
		t.Fatal(err)
	}
	if err := tf.fwd.pause(); err == nil || !strings.Contains(err.Error(), "already paused") {
		t.Errorf("expected an error for a second pause, got %v", err)
	}
}
