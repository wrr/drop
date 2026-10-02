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

package netproxy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"
)

// Intercepted connections. In the sandbox network namespace all IPv4
// addresses are local, so a connection to any address and an allowed
// port is accepted by a listener the proxy serves. The local address
// of such a connection is the destination the sandboxed process
// connected to. This works for any protocol over TCP, sandboxed
// programs don't need to be configured to use the proxy.
//
// A connection is allowed if its destination address was returned by
// the DNS server for a domain allowed on the destination port, or if
// the address is explicitly listed in allowed_domains.

// ServeTransparent accepts intercepted connections on l until Close
// is called.
func (p *Proxy) ServeTransparent(l net.Listener) error {
	if !p.addCloser(l) {
		return nil
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			if p.isClosed() {
				return nil
			}
			// For example, too many open files. Don't stop serving,
			// retry after the condition clears.
			p.logger.Printf("accept intercepted connection: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr)
		if !ok {
			conn.Close()
			continue
		}
		go p.handleTransparent(conn, tcpAddr.AddrPort())
	}
}

// handleTransparent connects the client to dst, if allowed.
func (p *Proxy) handleTransparent(client net.Conn, dst netip.AddrPort) {
	dst = netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())
	target := dst.String()
	host, ok := p.transparentHost(dst.Addr(), int(dst.Port()))
	if !ok {
		p.logger.Printf("denied TCP %s: no allowed domain resolves to this address", target)
		reset(client)
		return
	}

	upstream, err := p.dialHost(context.Background(), host, "tcp", target)
	if err != nil {
		if errors.Is(err, errForbiddenAddr) {
			p.logger.Printf("denied TCP %s (%s): %v", host, target, err)
		} else {
			p.logger.Printf("failed TCP %s (%s): %v", host, target, err)
		}
		reset(client)
		return
	}
	p.logger.Printf("allowed TCP %s (%s)", host, target)
	p.pipe(client, client, upstream)
}

// transparentHost returns the host on behalf of which a connection
// to addr:port is allowed: a domain that resolved to addr or addr
// itself if it is listed in allowed_domains.
func (p *Proxy) transparentHost(addr netip.Addr, port int) (string, bool) {
	for _, name := range p.resolvedNames(addr) {
		if p.allow.Allows(name, port) {
			return name, true
		}
	}
	if p.allow.Allows(addr.String(), port) {
		return addr.String(), true
	}
	return "", false
}

// reset closes the connection with TCP RST, so the client gets
// "connection reset" error instead of a regular end of data.
func reset(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetLinger(0)
	}
	c.Close()
}
