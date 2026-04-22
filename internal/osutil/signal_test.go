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
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
)

// TestCatchAndForwardSignals verifies that a catchable
// signal delivered to the parent is forwarded to the child the
// child terminates with that signal, but the parent does not
func TestCatchAndForwardSignals(t *testing.T) {
	t.Cleanup(func() {
		signal.Reset(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	})

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	cleanup := CatchAndForwardSignals(cmd.Process)
	defer cleanup()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM to self: %v", err)
	}

	err := cmd.Wait()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("expected syscall.WaitStatus, got %T", exitErr.Sys())
	}
	if !ws.Signaled() {
		t.Fatalf("expected child terminated by signal, got exit status %v", ws.ExitStatus())
	}
	if ws.Signal() != syscall.SIGTERM {
		t.Fatalf("child terminated by %v, want SIGTERM", ws.Signal())
	}
}
