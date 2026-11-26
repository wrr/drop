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
	"log/slog"
	"strings"
	"testing"

	"github.com/wrr/drop/internal/pty"
)

// logLine returns the line logged by an actionLogger prepared with
// logAction, without the time.
func logLine(logAction func(a *actionLogger)) string {
	var b strings.Builder
	h := slog.NewTextHandler(&b, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	})
	a := newFirewallLogger(slog.New(h)).newActionLogger()
	logAction(a)
	a.done()
	return strings.TrimSuffix(b.String(), "\n")
}

func TestActionLogLine(t *testing.T) {
	cases := []struct {
		logAction func(a *actionLogger)
		want      string
	}{
		{
			func(a *actionLogger) {
				a.domain("example.com")
				a.setReason("ask fallback: prompts unavailable")
				a.action(Allow)
			},
			`level=INFO msg=dns domain=example.com action=allow reason="ask fallback: prompts unavailable"`,
		},
		{
			func(a *actionLogger) {
				a.destination(testDst)
				a.domains([]string{"a.com", "b.com"})
				a.promptChoice(pty.Choice{Key: 'D'})
				a.action(Deny)
			},
			"level=INFO msg=connect destination=192.0.2.1:443/tcp domains=a.com,b.com prompt=D action=deny",
		},
		{
			func(a *actionLogger) { a.denyWithReason("parse destination: truncated tcp header") },
			`level=INFO msg=connect action=deny reason="parse destination: truncated tcp header"`,
		},
	}
	for _, c := range cases {
		if got := logLine(c.logAction); got != c.want {
			t.Errorf("got  %s\nwant %s", got, c.want)
		}
	}
}
