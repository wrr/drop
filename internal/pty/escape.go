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
	// DSR, the terminal replies with CSI row;col R.
	cursorPositionQuery = csi + "6n"
)

// moveTo returns the sequence that moves the cursor to the row and
// column, both 0-based. The sequence itself is 1-based.
func moveTo(row, col int) string {
	return fmt.Sprintf(csi+"%d;%dH", row+1, col+1)
}

// scrollUp returns the sequence that scrolls the content up by n
// rows (SU). If the program set a scroll region (DECSTBM),
// only the rows within the region scroll.
func scrollUp(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(csi+"%dS", n)
}

// scrollDown returns the sequence that scrolls the content down by n
// rows (SD).
func scrollDown(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(csi+"%dT", n)
}
