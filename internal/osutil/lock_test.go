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
	"path/filepath"
	"testing"
)

func expectLocked(t *testing.T, path string, want bool) {
	t.Helper()
	locked, err := IsFileLocked(path)
	if err != nil {
		t.Fatalf("IsFileLocked: %v", err)
	}
	if locked != want {
		t.Errorf("IsFileLocked = %v, want %v", locked, want)
	}
}

func TestLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	expectLocked(t, path, false)

	file, err := LockFile(path)
	if err != nil {
		t.Fatalf("LockFile: %v", err)
	}
	expectLocked(t, path, true)
	if _, err := LockFile(path); err == nil {
		t.Error("LockFile of a locked file: expected an error")
	}

	file.Close()
	expectLocked(t, path, false)
	file, err = LockFile(path)
	if err != nil {
		t.Fatalf("LockFile after release: %v", err)
	}
	file.Close()
}
