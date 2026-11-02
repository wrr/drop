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
	"bytes"
	"errors"
	"os"
	"sync"
	"time"
)

const (
	// pausedInputDiscard specifies a brief time for which the terminal
	// input is discarded after forwarding is paused. This prevents keys
	// pressed right after the interactive prompt is shown, likely still
	// intended for the sandbox, from being captured and interpreted as
	// the prompt action.
	pausedInputDiscard = 700 * time.Millisecond
	// How many bytes of input a paused forwarder will capture reliably.
	maxCapturedInput = 16
)

// forwarder relays input and output between the terminal from the
// host and the PTY of the sandboxed process. The relaying can be
// paused, so Drop can take over the terminal, display an interactive
// prompt and capture the user's answer.
type forwarder struct {
	sandboxPty *os.File
	hostOutput *os.File
	// Nil if the process input is not a terminal.
	hostInput *os.File

	stopped chan struct{}

	mu sync.Mutex
	// Closed when forwarding is paused, replaced on resume. Not set to
	// nil on pause, so a goroutine that checked the state just before
	// the pause doesn't block on it forever.
	paused chan struct{}
	// Both channels below are nil when not paused.
	resumed chan struct{}
	// Receives input captured while forwarding is paused.
	capturedInput chan byte
	discardUntil  time.Time
}

// newForwarder creates forwarder and takes ownership of sandboxPty.
func newForwarder(sandboxPty, hostOutput, hostInput *os.File) *forwarder {
	fwd := &forwarder{
		sandboxPty: sandboxPty,
		hostOutput: hostOutput,
		hostInput:  hostInput,
		stopped:    make(chan struct{}),
		paused:     make(chan struct{}),
	}
	if hostInput != nil {
		// sandboxed pty <- host term
		go fwd.forwardInput()
	}
	// host term <- sandboxed pty
	go fwd.forwardOutput()
	return fwd
}

// stop permanently stops the forwarder. The forwarding goroutines
// aren't interrupted: they return after their next read, or when Drop
// exits.
func (f *forwarder) stop() {
	f.sandboxPty.Close()
	close(f.stopped)
}

// forwardInput copies the user input from the host terminal to the
// sandboxed process PTY. When the forwarder is paused the input no
// longer reaches the sandbox and can be retrieved with the readInput
// function.
func (f *forwarder) forwardInput() {
	// Writing is done in a separate goroutine, so when the sandbox
	// doesn't read its input and the write blocks, the forwarder can
	// still be paused and capture input.
	sandboxIn := make(chan []byte)
	defer close(sandboxIn)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for input := range sandboxIn {
			if _, err := f.sandboxPty.Write(input); err != nil {
				return
			}
		}
	}()

	buf := make([]byte, 1024)
	for {
		n, err := f.hostInput.Read(buf)
		if n > 0 {
			input := buf[:n]
			if f.routeInput(input) {
				select {
				case sandboxIn <- bytes.Clone(input):
				case <-f.pauseSignal():
				case <-writerDone:
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// pauseSignal returns a channel closed when forwarding is paused.
func (f *forwarder) pauseSignal() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paused
}

// routeInput returns true if the forwarding is not paused and the
// input should be passed to the sandboxed process. Otherwise it
// exposes the input to Drop via the readInput function.
func (f *forwarder) routeInput(input []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.isPaused() {
		return true
	}
	if time.Now().Before(f.discardUntil) {
		// Discard input entered immediately after the forwarding was
		// paused.
		return false
	}
	for _, b := range input {
		select {
		case f.capturedInput <- b:
		default:
			// No more space, drop remaining input, interactive prompt needs
			// only a single key.
			return false
		}
	}
	return false
}

// forwardOutput copies the sandboxed process output to the host
// terminal. When the forwarder is paused it stops reading the PTY,
// so the sandboxed process eventually blocks on write.
func (f *forwarder) forwardOutput() {
	buf := make([]byte, 32*1024)
	for {
		n, err := f.sandboxPty.Read(buf)
		if n > 0 {
			if f.writeOutput(buf[:n]) != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// writeOutput writes the output to the host terminal. It waits for
// paused forwarding to resume.
func (f *forwarder) writeOutput(output []byte) error {
	f.mu.Lock()
	if !f.isPaused() {
		// Write with the lock to guarantee no sandbox output reaches
		// terminal after pause() returns.
		_, err := f.hostOutput.Write(output)
		f.mu.Unlock()
		return err
	}
	resumed := f.resumed
	f.mu.Unlock()
	select {
	case <-resumed:
		// Check if not paused again and only then write
		return f.writeOutput(output)
	case <-f.stopped:
		return errors.New("forwarder stopped")
	}
}

// pause pauses the forwarding and gives Drop the terminal until
// resume is called. The sandboxed process keeps running, but its
// output is no longer read (it blocks once the PTY buffer is
// full). The user input is no longer passed to the sandbox, but
// exposed to Drop via readInput.
func (f *forwarder) pause() error {
	if f.hostInput == nil {
		return errors.New("stdin is not a terminal")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isPaused() {
		return errors.New("forwarding already paused")
	}
	close(f.paused)
	f.discardUntil = time.Now().Add(pausedInputDiscard)
	f.resumed = make(chan struct{})
	f.capturedInput = make(chan byte, maxCapturedInput)
	return nil
}

// isPaused reports whether forwarding is paused. f.mu must be held.
func (f *forwarder) isPaused() bool {
	return f.capturedInput != nil
}

// resume resumes the forwarding.
func (f *forwarder) resume() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.isPaused() {
		return
	}
	close(f.resumed)
	f.resumed = nil
	f.paused = make(chan struct{})
	close(f.capturedInput)
	f.capturedInput = nil
}

// readInput returns one byte of the user input while forwarding is
// paused. It can be called multiple times, but only maxCapturedInput
// bytes are guaranteed to be captured reliably, further input can be
// lost. The call blocks until input is available or forwarding is
// resumed or stopped.
func (f *forwarder) readInput() (byte, error) {
	f.mu.Lock()
	ch := f.capturedInput
	f.mu.Unlock()
	if ch == nil {
		return 0, errors.New("forwarding not paused")
	}
	select {
	case b, ok := <-ch:
		if !ok {
			return 0, errors.New("forwarding resumed")
		}
		return b, nil
	case <-f.stopped:
		return 0, errors.New("forwarder stopped")
	}
}
