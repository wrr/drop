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

package jailfs

import (
	"fmt"
	"os"
	"strings"
)

// CwdInSandbox returns the working directory of the sandboxed process:
// the first of the cwdCandidates that exists as a directory that can
// be entered within the assembled root filesystem (fsRoot).
//
// CwdInSandbox must be called only after ArrangeFilesystem has
// assembled fsRoot and capabilities have been dropped.
func CwdInSandbox(fsRoot string, cwdCandidates []string) (string, error) {
	root, err := os.OpenRoot(fsRoot)
	if err != nil {
		return "", fmt.Errorf("open sandbox root %s: %v", fsRoot, err)
	}
	defer root.Close()

	for _, dir := range cwdCandidates {
		name := strings.TrimPrefix(dir, "/") // Root wants a root-relative name
		if name == "" {
			name = "."
		}
		// The trailing "." makes the stat fail if the candidate is not
		// a directory or cannot be entered.
		if _, err := root.Stat(name + "/."); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("failed to obtain CWD for the sandbox root %s", fsRoot)
}
