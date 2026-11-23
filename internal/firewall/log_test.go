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
func logLine(mode logMode, logAction func(a *actionLogger)) string {
	var b strings.Builder
	h := slog.NewTextHandler(&b, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	})
	a := newFirewallLogger(mode, slog.New(h)).newActionLogger()
	logAction(a)
	a.done()
	return strings.TrimSuffix(b.String(), "\n")
}

func TestParseLogMode(t *testing.T) {
	for s, want := range map[string]logMode{"none": logNone, "denied": logDenied, "all": logAll} {
		if got, err := parseLogMode(s); err != nil || got != want {
			t.Errorf("parseLogMode(%q) = %v, %v, want %v", s, got, err, want)
		}
	}
	if _, err := parseLogMode("invalid"); err == nil {
		t.Error(`parseLogMode("invalid"): expected an error`)
	}
}

func TestActionLogMode(t *testing.T) {
	allow := func(a *actionLogger) { a.action(Allow) }
	deny := func(a *actionLogger) { a.action(Deny) }
	noAction := func(a *actionLogger) {}
	cases := []struct {
		name      string
		mode      logMode
		logAction func(a *actionLogger)
		logged    bool
	}{
		{"none allow", logNone, allow, false},
		{"none deny", logNone, deny, false},
		{"denied allow", logDenied, allow, false},
		{"denied deny", logDenied, deny, true},
		{"denied no action", logDenied, noAction, false},
		{"all allow", logAll, allow, true},
		{"all deny", logAll, deny, true},
	}
	for _, c := range cases {
		if got := logLine(c.mode, c.logAction) != ""; got != c.logged {
			t.Errorf("%s: logged = %v, want %v", c.name, got, c.logged)
		}
	}
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
		if got := logLine(logAll, c.logAction); got != c.want {
			t.Errorf("got  %s\nwant %s", got, c.want)
		}
	}
}
