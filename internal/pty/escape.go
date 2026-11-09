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

package pty

import "fmt"

// Terminal control sequences.
const (
	esc           = "\x1b"
	csi           = esc + "["  // control sequence introducer
	saveCursor    = esc + "7"  // DECSC saves the cursor, the colors and the charset
	restoreCursor = esc + "8"  // DECRC
	selectASCII   = "\x0f"     // SI
	g0ASCII       = esc + "(B" // SCS loads ASCII into G0
	eraseLine     = csi + "2K"
	resetStyle    = csi + "0m"
	promptStyle   = csi + "1;33m" // Bold with yellow foreground
)

func moveTo(row, col int) string {
	return fmt.Sprintf(csi+"%d;%dH", row, col)
}
