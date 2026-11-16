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
	"os"
	"os/signal"
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
	return term.IsTerminal(0) || term.IsTerminal(1) || term.IsTerminal(2)
}

// AllFdsAreTerminal returns true if all of stdin, stdout, stderr are
// terminals.
func AllFdsAreTerminal() bool {
	return term.IsTerminal(0) && term.IsTerminal(1) && term.IsTerminal(2)
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

// ForwardPty forwards:
// * terminal input (if any) of the calling process to the sandboxPty
// * sandboxPty terminal output to the calling process terminal
// * terminal window size changes from the calling process to the sandboxPty
// The function takes ownership of sandboxPty.
// The function returns error if none of stdin, stdout,
// stderr of the calling process is a terminal (PtyNeeded returns false).
func ForwardPty(sandboxPty *os.File) (*Prompter, func(), error) {
	var (
		origState *term.State
		resized   chan os.Signal
		fwd       *forwarder
		err       error
	)

	cleanup := func() {
		if resized != nil {
			signal.Stop(resized)
		}
		if fwd != nil {
			fwd.stop()
		} else {
			sandboxPty.Close()
		}
		if origState != nil {
			term.Restore(0, origState)
		}
	}
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()

	if !PtyNeeded() {
		return nil, nil, fmt.Errorf("none of stdin, stdout, stderr is a terminal")
	}

	if term.IsTerminal(0) {
		// Only terminal used for input must be switched to raw mode.
		origState, err = term.MakeRaw(0)
		if err != nil {
			return nil, nil, fmt.Errorf("switch terminal to raw mode: %v", err)
		}
	}
	var outputTerm *os.File

	// Find terminal to output to. In a rare case when stdout and
	// stderr are different terminals, all Drop output goes to the
	// terminal associated with stdout.
	if term.IsTerminal(1) {
		outputTerm = os.Stdout
	} else if term.IsTerminal(2) {
		outputTerm = os.Stderr
	} else {
		// stdout and stderr are not terminals, so stdin must be.
		// Write PTY output (primarily input echo) back to stdin,
		// which works because terminal devices are bidirectional.
		outputTerm = os.Stdin
	}
	var inputTerm *os.File
	if term.IsTerminal(0) {
		inputTerm = os.Stdin
	}

	resized = make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	fwd, err = newForwarder(sandboxPty, outputTerm, inputTerm, resized)
	if err != nil {
		return nil, nil, err
	}
	success = true
	return newPrompter(fwd), cleanup, nil
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
