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
	"strings"
	"unicode/utf8"

	"github.com/wrr/drop/internal/pty"
)

var promptChoices = []pty.Choice{
	{Key: 'a', Label: "[a]llow", Answer: "allowed"},
	{Key: 'd', Label: "[d]eny", Answer: "denied"},
	{Key: 'A', Label: "[A]llow always", Answer: "always allowed"},
	{Key: 'D', Label: "[D]eny always", Answer: "always denied"},
}

// prompter shows questions to the user.
type prompter interface {
	// Ask shows the question with given choices. The question lines are
	// formatted by formatQuestion to fit within maxWidth. Returns the
	// choice selected by the user.
	Ask(formatQuestion func(maxWidth int) []string, choices []pty.Choice) (pty.Choice, error)
}

// resolverQuestion returns the question lines for a DNS query domain,
// with the domain shortened to fit maxWidth.
//
// The fixed text is not shortened. On terminals too narrow for "Allow
// connections to", the user is informed that the terminal is too small.
func resolverQuestion(domain string, maxWidth int) []string {
	line := "Allow connections to " + domain + "?"
	if utf8.RuneCountInString(line) <= maxWidth {
		return []string{line}
	}
	const indent = "  "
	lines := []string{
		"Allow connections to",
		indent + shorten(domain, maxWidth-len(indent)-len("?")) + "?",
	}
	return lines
}

// connectionQuestion returns the question lines for a new connection
// to the destination d.
// domains, which resolved to d's address in the order given, are
// shown as context, the most recently resolved first.
func connectionQuestion(d destination, domains []string, maxWidth int) []string {
	addr := d.addr.String()
	details := ""
	if d.port != 0 {
		details += fmt.Sprintf(" port %d", d.port)
	}
	if d.proto != "" {
		details += "/" + d.proto
	}
	const indent = "  "
	lines := []string{fmt.Sprintf("Allow connections to %s%s?", addr, details)}
	if utf8.RuneCountInString(lines[0]) > maxWidth {
		lines = []string{
			"Allow connections to",
			indent + addr,
		}
		if len(details) > 0 {
			lines = append(lines, indent+strings.TrimSpace(details))
		}
		lines[len(lines)-1] += "?"
	}
	prefix := indent + "Address of: "
	const maxDomains = 7
	for i := 0; i < len(domains) && i < maxDomains; i++ {
		item := domains[len(domains)-1-i]
		if i == maxDomains-1 && len(domains) > maxDomains {
			item = fmt.Sprintf("+%d more", len(domains)-i)
		}
		line := prefix + shorten(item, maxWidth-len(prefix))
		lines = append(lines, line)
		prefix = strings.Repeat(" ", len(prefix))
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
