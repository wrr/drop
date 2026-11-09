// Copyright 2025-2026 Jan Wrobel <jan@mixedbit.org>
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
	"os"
	"path/filepath"
)

// WriteResolvConf creates and writes resolv.conf file in the passed
// directory.
func WriteResolvConf(etcDir, dnsAddr, options string) error {
	resolvConfPath := filepath.Join(etcDir, "resolv.conf")
	content := "nameserver " + dnsAddr + "\n"
	if options != "" {
		content += "options " + options + "\n"
	}
	return os.WriteFile(resolvConfPath, []byte(content), 0600)
}
