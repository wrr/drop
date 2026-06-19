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
	"encoding/gob"
	"fmt"
	"os"

	"golang.org/x/sys/unix"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/jailfs"
)

type ParentEnd struct {
	socket *os.File
}

type ChildEnd struct {
	// Public, so parent process can pass it to the executed child as
	// ExtraFiles
	Socket *os.File
}

// ChildArgs contains arguments needed by both parent and child that
// the parent constructs and sends to the child.
//
// Note for future extensions: unexported fields or interface types if
// included within ChildArgs chierarchy will not be encoded and sent
// (encoding/gob limitation).
type ChildArgs struct {
	EnvId    string
	Paths    *jailfs.Paths
	Config   *config.Config
	ExecArgs []string
}

// NewParentChildSocket creates an anonymous socket that parent uses
// to send arguments to the child, which also signals to the child
// that parent has already setup networking.
func NewParentChildSocket() (*ParentEnd, *ChildEnd, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("create socket pair: %v", err)
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

// SendChildArgs serializes and sends to the child all the necessary
// arguments and configuration options obtained by the parent from
// command line and from config files.
//
// Parent sends the arguments after all the necessary setup needed by
// the child is finished (network setup is done), so the child can
// assume that after the arguments are received, a sandboxed process
// can be launched.
func (p *ParentEnd) SendChildArgs(args ChildArgs) error {
	if err := gob.NewEncoder(p.socket).Encode(args); err != nil {
		return fmt.Errorf("send arguments to child: %v", err)
	}
	return nil
}

func (p *ParentEnd) Close() error {
	if p.socket != nil {
		err := p.socket.Close()
		p.socket = nil
		return err
	}
	return nil
}

// RecvChildArgs receives arguments sent by the parent process to the
// child. The function blocks until the arguments are available.
func (c *ChildEnd) RecvChildArgs() (*ChildArgs, error) {
	childArgs := ChildArgs{}
	if err := gob.NewDecoder(c.Socket).Decode(&childArgs); err != nil {
		return nil, fmt.Errorf("receive arguments from parent: %v", err)
	}
	return &childArgs, nil
}

func (c *ChildEnd) Close() error {
	if c.Socket != nil {
		err := c.Socket.Close()
		c.Socket = nil
		return err
	}
	return nil
}
