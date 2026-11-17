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
	"fmt"
	"os"
	"syscall"
)

// lockFile locks the file with path. Return a file handle which should
// be closed to release the lock.
func LockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("create a file: %v", err)
	}
	// Do not close the file, as this releases the lock. The lock should
	// be released when the process terminates.

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return nil, fmt.Errorf("lock a file %v: %v", path, err)
	}
	return file, nil
}

// IsFileLocked returns true if the file with path is locked.
func IsFileLocked(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Lock file doesn't exists,  not locked
			return false, nil
		}
		return false, err
	}
	defer file.Close()
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// File is locked by another process or another error
		// (we just assume file is locked to avoid complex error
		//  handling).
		return true, nil
	}
	// Not locked, release the lock
	syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false, nil

}
