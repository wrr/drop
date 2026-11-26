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
	"context"
	"log/slog"
	"strings"

	"github.com/wrr/drop/internal/pty"
)

type firewallLogger struct {
	*slog.Logger
}

func (l *firewallLogger) newActionLogger() *actionLogger {
	return &actionLogger{fw: l}
}

func newFirewallLogger(log *slog.Logger) *firewallLogger {
	return &firewallLogger{Logger: log}
}

type actionLogger struct {
	fw *firewallLogger
	// Set for DNS queries, connections use dst.
	query       bool
	queryDomain string
	dst         destination
	// Domains that resolved to dst's address.
	resolvedFrom []string
	prompt       *pty.Choice
	act          *Action
	reason       string
}

func (a *actionLogger) denyWithReason(reason string) {
	a.setReason(reason)
	a.action(Deny)
}

func (a *actionLogger) setReason(reason string) {
	a.reason = reason
}

func (a *actionLogger) action(action Action) {
	a.act = &action
}

func (a *actionLogger) destination(dst destination) {
	a.dst = dst
}

func (a *actionLogger) domain(domain string) {
	a.query = true
	a.queryDomain = domain
}

func (a *actionLogger) domains(domains []string) {
	a.resolvedFrom = domains
}

func (a *actionLogger) promptChoice(c pty.Choice) {
	a.prompt = &c
}

func (a *actionLogger) done() {
	msg := "connect"
	var attrs []slog.Attr
	if a.query {
		msg = "dns"
		attrs = append(attrs, slog.String("domain", a.queryDomain))
	} else if a.dst.hasAddr() {
		attrs = append(attrs, slog.String("destination", a.dst.String()))
	}
	if len(a.resolvedFrom) > 0 {
		attrs = append(attrs, slog.String("domains", strings.Join(a.resolvedFrom, ",")))
	}
	if a.prompt != nil {
		attrs = append(attrs, slog.String("prompt", string(a.prompt.Key)))
	}
	if a.act != nil {
		attrs = append(attrs, slog.String("action", a.act.String()))
	}
	if a.reason != "" {
		attrs = append(attrs, slog.String("reason", a.reason))
	}
	a.fw.LogAttrs(context.Background(), slog.LevelInfo, msg, attrs...)
}
