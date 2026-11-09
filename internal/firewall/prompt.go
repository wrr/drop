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

package firewall

import (
	"fmt"
	"net/netip"
	"unicode/utf8"

	"github.com/wrr/drop/internal/pty"
)

var promptChoices = []pty.Choice{
	{Key: 'a', Label: "[a]llow", Answer: "allowed"},
	{Key: 'd', Label: "[d]eny", Answer: "denied"},
	{Key: 'A', Label: "[A]llow always", Answer: "always allowed"},
	{Key: 'D', Label: "[D]eny always", Answer: "always denied"},
}

// dialog shows questions to the user.
type dialog interface {
	// MaxWidth returns how long question lines can be, longer lines make
	// Ask fail.
	MaxWidth() int
	Ask(question []string, choices []pty.Choice) (pty.Choice, error)
}

// question returns the question lines, with the domain shortened to
// fit maxWidth.
//
// The fixed text is not shortened. On terminals too narrow for
// "Allow connections to", the dialog fails and the ask_fallback
// setting applies.
func question(domain string, addr netip.Addr, port int, proto string, maxWidth int) []string {
	target := domain
	if target == "" {
		target = addr.String()
	}
	details := ""
	if port != 0 {
		// The decision covers all ports, the port and proto are only a context.
		details = fmt.Sprintf("(attempted: port %d/%s)", port, proto)
	}
	line := "Allow connections to " + target + "?"
	if details != "" {
		line += " " + details
	}
	if utf8.RuneCountInString(line) <= maxWidth {
		return []string{line}
	}
	const indent = "  "
	lines := []string{
		"Allow connections to",
		indent + shorten(target, maxWidth-len(indent)-len("?")) + "?",
	}
	if details != "" {
		lines = append(lines, indent+details)
	}
	return lines
}

// shorten cuts out the start of s to make it at most width long.
func shorten(s string, width int) string {
	const rpl = "[...]"
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	r := []rune(s)
	if width < len(rpl) {
		return string(r[len(r)-max(width, 0):])
	}
	return rpl + string(r[len(r)-(width-len(rpl)):])
}
