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

	"github.com/wrr/drop/internal/pty"
)

var promptChoices = []pty.Choice{
	{Key: 'a', Label: "[a]llow", Answer: "allowed"},
	{Key: 'A', Label: "[A]lways allow", Answer: "always allowed"},
	{Key: 'd', Label: "[d]eny", Answer: "denied"},
	{Key: 'D', Label: "[D]eny always", Answer: "always denied"},
}

// dialog shows questions to the user.
type dialog interface {
	Ask(question string, choices []pty.Choice) (pty.Choice, error)
}

func question(domain string, addr netip.Addr, port int, proto string) string {
	target := domain
	if target == "" {
		target = addr.String()
	}
	question := fmt.Sprintf("allow connections to %s?", target)
	if port != 0 {
		// The decision covers all ports, the port and proto are only a context.
		question += fmt.Sprintf(" (attempted: port %d/%s)", port, proto)
	}
	return question
}
