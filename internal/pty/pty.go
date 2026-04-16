// Copyright 2025 Jan Wrobel <jan@mixedbit.org>
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
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func ptyError(format string, a ...any) error {
	return fmt.Errorf("PTY setup: "+format, a...)
}

// NewPty creates a new pseduterminal and returns its parent and child
// file descriptors.
func NewPty() (*os.File, *os.File, error) {
	parent, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, ptyError("%v", err)
	}
	parentFd := int(parent.Fd())

	// Unlock the child PTY
	if err := unix.IoctlSetPointerInt(parentFd, unix.TIOCSPTLCK, 0); err != nil {
		parent.Close()
		return nil, nil, ptyError("unlock PTY: %v", err)
	}

	// Get the child PTY number
	ptyNum, err := unix.IoctlGetInt(parentFd, unix.TIOCGPTN)
	if err != nil {
		parent.Close()
		return nil, nil, ptyError("get PTY number: %v", err)
	}
	childPath := fmt.Sprintf("/dev/pts/%d", ptyNum)

	child, err := os.OpenFile(childPath, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		parent.Close()
		return nil, nil, ptyError("%v", err)
	}

	return parent, child, nil
}

// PtyNeeded returns true if any of the stdin, stdout, stderr is a
// terminal. For descriptors that are terminals, Sandbox needs to
// create own PTY instead of using the original, not sandboxed
// terminal.
func PtyNeeded() bool {
	return slices.ContainsFunc([]int{0, 1, 2}, term.IsTerminal)
}

// SetControllingTerminal sets the pty as the controlling terminal for
// the current process. The process must be a session leader (call
// setsid() first).
func SetControllingTerminal(pty *os.File) error {
	err := unix.IoctlSetPointerInt(int(pty.Fd()), unix.TIOCSCTTY, 0)
	if err != nil {
		return fmt.Errorf("set controlling terminal: %v", err)
	}
	return nil
}

// syncWindowSize copies the window size from srcFd to dstFd.
func syncWindowSize(srcFd, dstFd int) error {
	ws, err := unix.IoctlGetWinsize(srcFd, unix.TIOCGWINSZ)
	if err != nil {
		return err
	}
	return unix.IoctlSetWinsize(dstFd, unix.TIOCSWINSZ, ws)
}

// keepWindowSizeSynced propagates terminal window size from the
// current process to the termToSync. The function starts a goroutine
// to propagate also all future terminal window size changes.
// Returns error if none of stdin, stdout, stderr is a terminal
// (PtyNeeded returns false).
//
// Returns a cleanup function that should be called when program exits.
func keepWindowSizeSynced(termToSync *os.File) (func(), error) {
	syncFrom := slices.IndexFunc([]int{0, 1, 2}, term.IsTerminal)
	if syncFrom == -1 {
		return nil, fmt.Errorf("sync window size: none of stdin, stdout, stderr is a terminal")
	}
	syncTo := int(termToSync.Fd())
	if err := syncWindowSize(syncFrom, syncTo); err != nil {
		return nil, fmt.Errorf("sync window size: %v", err)
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	go func() {
		for range sigCh {
			// Ignore errors
			syncWindowSize(syncFrom, syncTo)
		}
	}()

	cleanup := func() {
		signal.Stop(sigCh)
		close(sigCh)
	}
	return cleanup, nil
}

// ForwardPty forwards:
// * terminal input (if any) of the calling process to the dstPty
// * dstPty terminal output to the calling process terminal
// * terminal windows size changes from the calling process to the dstPty
// The function takes ownership of dstPty.
// This function returns error if none of stdin, stdout,
// stderr of the calling process is a terminal (PtyNeeded returns false).
func ForwardPty(dstPty *os.File) (func(), error) {
	var (
		origState        *term.State
		cleanWinSizeSync func()
		err              error
	)

	cleanup := func() {
		if cleanWinSizeSync != nil {
			cleanWinSizeSync()
		}
		if origState != nil {
			term.Restore(0, origState)
		}
		// Terminates io.Copy goroutine that reads from dstPty,

		// The second io.Copy goroutine that reads from os.Stdin can keep
		// running until program terminates unless there is some os.Stdin
		// input.
		dstPty.Close()
	}

	if term.IsTerminal(0) {
		// Only terminal used for input must be switched to raw mode.
		origState, err = term.MakeRaw(0)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("switch terminal to raw mode: %v", err)
		}
	}
	cleanWinSizeSync, err = keepWindowSizeSynced(dstPty)
	if err != nil {
		cleanup()
		return nil, err
	}

	// Start I/O forwarding goroutines
	if term.IsTerminal(0) {
		// sandboxed term <- host term
		go io.Copy(dstPty, os.Stdin)
	}
	// host term <- sandboxed term
	var outputTerm *os.File

	// Find terminal to output to. In a rare case when stdout and
	// stderr are different terminals, all Drop output goes to the
	// terminal associated with stdout.
	if term.IsTerminal(1) {
		outputTerm = os.Stdout
	} else if term.IsTerminal(2) {
		outputTerm = os.Stderr
	} else {
		// stdout and stderr are not terminals, but stdin is.
		// Write PTY output (primarily input echo) back to stdin,
		// which works because terminal devices are bidirectional.
		outputTerm = os.Stdin
	}
	// host term <- sandboxed term
	go io.Copy(outputTerm, dstPty)

	return cleanup, nil
}

// ReplaceTerminal finds which of the stdin, stdout, stderr descriptors
// point to a terminal, and changes these descriptors to point to the
// new pseudoterminal ptyToUse.
func ReplaceTerminal(ptyToUse *os.File) error {
	for _, i := range []int{0, 1, 2} {
		if term.IsTerminal(i) {
			if err := unix.Dup3(int(ptyToUse.Fd()), i, 0); err != nil {
				return fmt.Errorf("replace terminal: point fd %d to the new PTY: %v", i, err)
			}
		}
	}
	return nil
}
