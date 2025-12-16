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

// Package ipc provides communication between parent and child
// processes that setup Drop sandbox. The communication is via a Unix
// domain socket.
package ipc

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

type ParentEnd struct {
	socket *os.File
}

type ChildEnd struct {
	// Public, so parent process can pass it to the executed child as
	// ExtraFiles
	Socket *os.File
}

func NewParentChildSocket() (*ParentEnd, *ChildEnd, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create socket pair: %v", err)
	}
	return NewParentEnd(uintptr(fds[0])), NewChildEnd(uintptr(fds[1])), nil
}

func NewParentEnd(fd uintptr) *ParentEnd {
	return &ParentEnd{
		socket: os.NewFile(fd, "parent-socket"),
	}
}

func NewChildEnd(fd uintptr) *ChildEnd {
	return &ChildEnd{
		Socket: os.NewFile(fd, "child-socket"),
	}
}

// NotifyNetworkReady notifies the child that parent network setup has finished.
func (p *ParentEnd) NotifyNetworkReady() error {
	if _, err := p.socket.Write([]byte{1}); err != nil {
		return fmt.Errorf("failed to send network ready notification: %v", err)
	}
	return nil
}

func recvPtyError(format string, a ...any) error {
	return fmt.Errorf("recv pty: "+format, a...)
}

// RecvPty receives a parent descriptor of a sandboxed pseudoterminal
// and wraps it into os.File
func (p *ParentEnd) RecvPty() (*os.File, error) {
	buf := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err := unix.Recvmsg(int(p.socket.Fd()), buf, oob, 0)
	if err != nil {
		return nil, recvPtyError("recvmsg: %v", err)
	}
	// Recvmsg has a bug and does not propagate err correctly,
	// so we detect EOF manually.
	// https://github.com/golang/go/issues/58898
	if n == 0 && oobn == 0 {
		return nil, io.EOF
	}
	scms, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return nil, recvPtyError("parse socket control message: %v", err)
	}
	if len(scms) != 1 {
		return nil, recvPtyError("expected 1 socket control message, got %d", len(scms))
	}
	fds, err := unix.ParseUnixRights(&scms[0])
	if err != nil {
		return nil, recvPtyError("parse unix rights: %v", err)
	}
	if len(fds) != 1 {
		return nil, recvPtyError("expected 1 fd, got %d", len(fds))
	}
	return os.NewFile(uintptr(fds[0]), "pty"), nil
}

func (p *ParentEnd) Close() error {
	if p.socket != nil {
		err := p.socket.Close()
		p.socket = nil
		return err
	}
	return nil
}

// WaitNetworkReady blocks until the parent notifies the network setup has finished.
func (c *ChildEnd) WaitNetworkReady() error {
	buf := make([]byte, 1)
	if _, err := c.Socket.Read(buf); err != nil {
		return fmt.Errorf("failed to wait for network ready: %v", err)
	}
	return nil
}

// SendPty sends parent descriptor of a sandboxed
// pseudoterminal. Parent process uses this descriptor to stream input
// and output between the sandboxed and the original terminal.
func (c *ChildEnd) SendPty(f *os.File) error {
	rights := unix.UnixRights(int(f.Fd()))
	err := unix.Sendmsg(int(c.Socket.Fd()), []byte{0}, rights, nil, 0)
	if err != nil {
		return fmt.Errorf("failed to send pty file descriptor to parent: %v", err)
	}
	return nil
}

func (c *ChildEnd) Close() error {
	if c.Socket != nil {
		err := c.Socket.Close()
		c.Socket = nil
		return err
	}
	return nil
}
