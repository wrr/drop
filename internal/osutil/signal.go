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

package osutil

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// CatchAndForwardSignals installs handlers for catchable shutdown
// signals. When such signal is delivered to the parent, it is
// forwarded to proc; if proc is still alive after 2 seconds it is
// killed with SIGKILL. Returns a function that should be called when
// process to which signals are forwarded terminates.
func CatchAndForwardSignals(proc *os.Process) func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	processTerminated := make(chan struct{})
	go func() {
		select {
		case sig := <-sigCh:
			// Errors are ignored: the process may have already exited.
			_ = proc.Signal(sig)
			select {
			case <-processTerminated:
			case <-time.After(1 * time.Second):
				_ = proc.Kill()
			}
		case <-processTerminated:
		}
	}()
	return func() {
		close(processTerminated)
	}
}
