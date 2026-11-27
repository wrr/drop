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
	"fmt"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// pausedInputDiscard specifies a brief time for which the terminal
	// input is discarded after forwarding is paused. This prevents keys
	// pressed right after the interactive prompt is shown, likely still
	// intended for the sandbox, from being captured and interpreted as
	// the prompt action.
	pausedInputDiscard = 700 * time.Millisecond
	// How many single key presses a paused forwarder will capture
	// reliably.
	maxCapturedKeys = 16
	// Some terminals do not reply to the cursor position query.
	defaultCursorQueryTimeout = 500 * time.Millisecond
)

var cursorPositionReport = regexp.MustCompile(`\x1b\[(\d+);(\d+)R`)

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
	// How long cursorRow waits for the reply.
	cursorQueryTimeout time.Duration

	mu     sync.Mutex
	paused chan struct{}
	// The channels below are nil when not paused.
	resumed chan struct{}
	// Receives keys captured while forwarding is paused.
	capturedKey chan byte
	// Receives a value when the host terminal is resized while
	// forwarding is paused.
	pausedResize chan struct{}
	discardUntil time.Time
	// Receives all input while cursorRow waits for the reply, nil
	// otherwise.
	cursorReport chan []byte
}

// newForwarder creates forwarder and, on success, takes ownership of
// sandboxPty. The host terminal size is copied to sandboxPty now and
// on each value received from resized.
func newForwarder(sandboxPty, hostOutput, hostInput *os.File, resized <-chan os.Signal) (*forwarder, error) {
	fwd := &forwarder{
		sandboxPty:         sandboxPty,
		hostOutput:         hostOutput,
		hostInput:          hostInput,
		stopped:            make(chan struct{}),
		paused:             make(chan struct{}),
		cursorQueryTimeout: defaultCursorQueryTimeout,
	}
	if err := fwd.copyWindowSize(); err != nil {
		return nil, fmt.Errorf("copy window size: %v", err)
	}
	if hostInput != nil {
		// sandboxed pty <- host term
		go fwd.forwardInput()
	}
	// host term <- sandboxed pty
	go fwd.forwardOutput()
	go fwd.forwardResizes(resized)
	return fwd, nil
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
// longer reaches the sandbox and can be retrieved with the waitEvent
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
				case <-f.pausedCh():
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

// pausedCh returns a channel closed when forwarding is paused.
func (f *forwarder) pausedCh() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paused
}

func sameBytes(input []byte) bool {
	for _, x := range input {
		if x != input[0] {
			return false
		}
	}
	return true
}

// routeInput returns true if the forwarding is not paused and the
// input should be passed to the sandboxed process. Otherwise it
// exposes the input to Drop via the waitEvent function, if the input
// is a single key, possibly pressed multiple times.
func (f *forwarder) routeInput(input []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.isPaused() {
		return true
	}
	if f.cursorReport != nil {
		// Keys pressed while waiting for the cursor position
		// are discarded by cursorRow.
		select {
		case f.cursorReport <- bytes.Clone(input):
		default:
		}
		return false
	}
	if time.Now().Before(f.discardUntil) {
		// Discard keys entered immediately after the forwarding was
		// paused.
		return false
	}
	if len(input) == 0 || !sameBytes(input) {
		// The dialog needs only a single key. Special keys (arrows,
		// function keys), mouse events and pastes arrive as multi-byte
		// reads, which are discarded, otherwise their individual bytes
		// could be interpreted as keys. Only single byte reads are
		// accepted and also repeated presses of the same key arriving in
		// one read.
		return false
	}
	select {
	case f.capturedKey <- input[0]:
	default:
		// No more space, the interactive prompt needs only a single key.
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

// forwardResizes copies the host terminal size to the sandboxed
// process PTY on each resize, until the forwarder is stopped. When
// paused, the resize is also reported by waitEvent.
func (f *forwarder) forwardResizes(resized <-chan os.Signal) {
	for {
		select {
		case <-resized:
			// Ignore errors
			f.copyWindowSize()
			f.notifyPausedResize()
		case <-f.stopped:
			return
		}
	}
}

// notifyPausedResize lets waitEvent return a resize.
func (f *forwarder) notifyPausedResize() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.isPaused() {
		return
	}
	select {
	case f.pausedResize <- struct{}{}:
	default:
		// A resize notification is already pending.
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
// exposed to Drop via waitEvent. Drop can also write to the terminal
// with the write function.
func (f *forwarder) pause() error {
	if f.hostInput == nil {
		return errors.New("stdin is not a terminal")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isPaused() {
		return errors.New("forwarding already paused")
	}
	// Not set to nil, so forwardInput doesn't block forever trying to
	// read from a nil channel.
	close(f.paused)
	f.discardUntil = time.Now().Add(pausedInputDiscard)
	f.resumed = make(chan struct{})
	f.capturedKey = make(chan byte, maxCapturedKeys)
	f.pausedResize = make(chan struct{}, 1)
	return nil
}

// isPaused reports whether forwarding is paused. f.mu must be held.
func (f *forwarder) isPaused() bool {
	return f.capturedKey != nil
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
	close(f.capturedKey)
	f.capturedKey = nil
	f.pausedResize = nil
}

// write writes to paused terminal.
func (f *forwarder) write(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.isPaused() {
		panic("write to not paused terminal")
	}
	_, err := f.hostOutput.WriteString(s)
	return err
}

// cursorRow returns the 0-based row of the host terminal cursor,
// asking the terminal with the cursor position query. Must be called
// when paused.
func (f *forwarder) cursorRow() (int, error) {
	reports := make(chan []byte, maxCapturedKeys)
	f.mu.Lock()
	if !f.isPaused() {
		f.mu.Unlock()
		return 0, errors.New("forwarding not paused")
	}
	f.cursorReport = reports
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.cursorReport = nil
		f.mu.Unlock()
	}()
	if err := f.write(cursorPositionQuery); err != nil {
		return 0, err
	}
	timeout := time.After(f.cursorQueryTimeout)
	// The reply can be split between reads.
	var input []byte
	for {
		select {
		case b := <-reports:
			input = append(input, b...)
			if m := cursorPositionReport.FindSubmatch(input); m != nil {
				row, err := strconv.Atoi(string(m[1]))
				if err != nil {
					return 0, fmt.Errorf("cursor position report %q: %v", m[0], err)
				}
				return row - 1, nil
			}
		case <-timeout:
			return 0, errors.New("no cursor position report")
		case <-f.stopped:
			return 0, errors.New("forwarder stopped")
		}
	}
}

// winSize returns the number of rows and columns of the host output
// terminal.
func (f *forwarder) winSize() (int, int, error) {
	size, err := getWinsize(f.hostOutput)
	if err != nil {
		return 0, 0, err
	}
	return int(size.Row), int(size.Col), nil
}

// waitEvent returns one key pressed by the user while forwarding is
// paused, or resized set to true if the host terminal was resized.
//
// Key delivery is simplistic, to avoid complications with
// multi-byte key parsing. It is only sufficient for the simple
// dialogs that expect the user to press a single, one-byte generating
// key:
// * Only keys that send a single byte are returned.
// * If multiple, different keys are pressed or pasted quickly, the
// whole such input chunk can be discarded, unless it contains only
// identical keys.
//
// The call blocks until key is available, the terminal is resized or
// forwarding is resumed or stopped.
func (f *forwarder) waitEvent() (key byte, resized bool, err error) {
	f.mu.Lock()
	keys := f.capturedKey
	resizes := f.pausedResize
	f.mu.Unlock()
	if keys == nil {
		return 0, false, errors.New("forwarding not paused")
	}
	select {
	case b, ok := <-keys:
		if !ok {
			return 0, false, errors.New("forwarding resumed")
		}
		return b, false, nil
	case <-resizes:
		return 0, true, nil
	case <-f.stopped:
		return 0, false, errors.New("forwarder stopped")
	}
}

// repaint sends signals to the sandboxed processes to redraw the
// terminal. Must be called when forwarder is not paused, so the
// repaint isn't withheld.
func (f *forwarder) repaint() error {
	size, err := getWinsize(f.sandboxPty)
	if err != nil {
		return err
	}
	if size.Row > 1 {
		size.Row--
	} else {
		size.Row++
	}
	if err := setWinsize(f.sandboxPty, size); err != nil {
		return err
	}
	// SIGWINCH does not queue. Without the delay the program can see
	// only the second signal with the original size, and skip the
	// redraw.
	time.Sleep(100 * time.Millisecond)
	// The current host size, in case the terminal was actually resized
	// during the sleep.
	return f.copyWindowSize()
}

// copyWindowSize copies the host terminal size to the sandboxed
// process PTY.
func (f *forwarder) copyWindowSize() error {
	ws, err := getWinsize(f.hostOutput)
	if err != nil {
		return err
	}
	return setWinsize(f.sandboxPty, ws)
}

// getWinsize returns the window size of the terminal file.
func getWinsize(file *os.File) (*unix.Winsize, error) {
	var ws *unix.Winsize
	err := control(file, func(fd int) error {
		var err error
		ws, err = unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
		return err
	})
	return ws, err
}

// setWinsize sets the window size of the terminal file.
func setWinsize(file *os.File, ws *unix.Winsize) error {
	return control(file, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
	})
}

// control calls fn with the Unix descriptor corresponding to the
// file. Unlike with Fd(), file can't be closed while fn runs.
func control(file *os.File, fn func(fd int) error) error {
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var fnErr error
	captureFnErr := func(fd uintptr) {
		fnErr = fn(int(fd))
	}
	if err := conn.Control(captureFnErr); err != nil {
		return err
	}
	return fnErr
}
