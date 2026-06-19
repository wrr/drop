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
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// PtyReceiver is created by the parent process to receive parent's
// descriptor of a sandboxed pseudoterminal. Parent process uses this
// descriptor to stream input and output between the sandboxed and the
// original terminal.
//
// Unlike the parent-child socket pair, this uses a named Unix domain
// socket so it can also receive the descriptor from runsc, which
// connects to and sends the pseudoterminal master over a named socket
// passed via --console-socket and does not support an unnamed socket
// pair.
type PtyReceiver struct {
	// path of the listening socket; unlinked on Close.
	path string
	// socket is the listening Unix domain socket wrapped in the File
	socket *os.File
}

// PtySender is created by the child process to send parent's descriptor
// of a sandboxed pseudoterminal.
type PtySender struct {
	// socketFd is a connected Unix domain socket
	socketFd int
}

// NewPtyReceiver creates a named Unix domain socket and listens on
// it.
//
// It is OK for the sender (the child, or runsc via --console-socket)
// to connect to the socket and send the descriptor before the parent
// calls RecvPty.
func NewPtyReceiver(path string) (*PtyReceiver, error) {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("create pty socket: %v", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("bind pty socket %s: %v", path, err)
	}
	// Restrict access to the owner only (extra measure, run dir where
	// the socket is placed is already restricted).
	if err := os.Chmod(path, 0o700); err != nil {
		unix.Close(fd)
		unix.Unlink(path)
		return nil, fmt.Errorf("chmod pty socket %s: %v", path, err)
	}

	// There is a single sender, so a backlog of one.
	if err := unix.Listen(fd, 1); err != nil {
		unix.Close(fd)
		unix.Unlink(path)
		return nil, fmt.Errorf("listen on pty socket %s: %v", path, err)
	}
	return &PtyReceiver{
		path:   path,
		socket: os.NewFile(uintptr(fd), "pty-listener"),
	}, nil
}

// RecvPty waits for the child to connect to the named Unix domain
// socket and to send file descriptor of the parent's end of the
// pseudoterminal.
func (p *PtyReceiver) RecvPty() (*os.File, error) {
	connFd, _, err := unix.Accept(int(p.socket.Fd()))
	if err != nil {
		return nil, recvPtyError("accept: %v", err)
	}
	defer unix.Close(connFd)

	buf := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err := unix.Recvmsg(connFd, buf, oob, 0)
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

// Close closes the listening socket and removes it from the filesystem.
func (p *PtyReceiver) Close() {
	if p.socket != nil {
		p.socket.Close()
		p.socket = nil
	}
	unix.Unlink(p.path)
}

// NewPtySender connects to the named Unix domain socket the parent
// listens on (path).
func NewPtySender(path string) (*PtySender, error) {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("create pty socket: %v", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		return nil, fmt.Errorf("connect to pty socket %s: %v", path, err)
	}
	return &PtySender{socketFd: fd}, nil
}

// SendPty sends f, the descriptor of the parent's end of the
// pseudoterminal. The parent uses this descriptor to stream input and
// output between the sandboxed and the original terminal.
func (p *PtySender) SendPty(f *os.File) error {
	rights := unix.UnixRights(int(f.Fd()))
	if err := unix.Sendmsg(p.socketFd, []byte{0}, rights, nil, 0); err != nil {
		return fmt.Errorf("send pty file descriptor: %v", err)
	}
	return nil
}

// Close closes the sender's end of the socket.
func (p *PtySender) Close() {
	unix.Close(p.socketFd)
}

func recvPtyError(format string, a ...any) error {
	return fmt.Errorf("recv pty: "+format, a...)
}
