// Copyright 2025 Jan Wrobel <jan@mixedbit.org>
//
// makeRaw and setONLCR functions are from
// https://github.com/containerd/console
// Copyright The containerd Authors.
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
	"golang.org/x/sys/unix"
	"os"
)

func ptyError(format string, a ...any) error {
	return fmt.Errorf("PTY setup error: "+format, a...)
}

// NewPty creates a new pseduterminal and returns its parent and child
// file descriptors.
func NewPty() (*os.File, *os.File, error) {
	parent, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, ptyError("failed to open /dev/ptmx: %w", err)
	}
	parentFd := int(parent.Fd())

	// Unlock the child PTY
	if err := unix.IoctlSetPointerInt(parentFd, unix.TIOCSPTLCK, 0); err != nil {
		parent.Close()
		return nil, nil, ptyError("failed to unlock PTY: %w", err)
	}

	// Get the child PTY number
	ptyNum, err := unix.IoctlGetInt(parentFd, unix.TIOCGPTN)
	if err != nil {
		parent.Close()
		return nil, nil, ptyError("failed to get PTY number: %w", err)
	}
	childPath := fmt.Sprintf("/dev/pts/%d", ptyNum)

	child, err := os.OpenFile(childPath, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		parent.Close()
		return nil, nil, ptyError("failed to open child PTY %s: %w", childPath, err)
	}
	childFd := int(child.Fd())

	// Set PTY to raw mode to disable echo and line processing
	termios, err := unix.IoctlGetTermios(childFd, unix.TCGETS)
	if err != nil {
		return nil, nil, ptyError("failed to get termios: %v", err)
	}
	makeRaw(termios)

	if err := unix.IoctlSetTermios(childFd, unix.TCSETS, termios); err != nil {
		return nil, nil, ptyError("failed to set raw mode: %v", err)
	}

	return parent, child, nil
}

func setONLCR(t *unix.Termios, enable bool) {
	if enable {
		// Set +onlcr so we can act like a real terminal
		t.Oflag |= unix.ONLCR
	} else {
		// Set -onlcr so we don't have to deal with \r.
		t.Oflag &^= unix.ONLCR
	}
}

func makeRaw(t *unix.Termios) {
	t.Iflag &^= (unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON)
	t.Oflag &^= unix.OPOST
	t.Lflag &^= (unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN)
	t.Cflag &^= (unix.CSIZE | unix.PARENB)
	t.Cflag |= unix.CS8
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
}
