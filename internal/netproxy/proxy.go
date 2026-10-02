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

// Package netproxy implements a proxy that allows sandboxed processes
// to connect only to allowed domains.
//
// In the "filtered" network mode the sandbox network namespace has
// only a loopback interface. The proxy sockets (DNS server and
// listeners for intercepted connections) are created within the
// sandbox namespace, but are served by the Drop parent process, which
// runs in the host network namespace and makes outbound connections on
// behalf of the sandbox. The proxy is thus the only way out of the
// sandbox.
package netproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"
)

// errForbiddenAddr is returned when an allowed domain resolves to
// an address that the domain rule doesn't allow (see checkAddr).
var errForbiddenAddr = errors.New("domain resolves to a non-public IP address")

// dialHostKey is a context key for the host (domain or IP address)
// requested by the client, passed to the dialer Control hook.
type dialHostKey struct{}

// cgnatPrefix is the shared address space (RFC 6598), used for
// example by Tailscale. netip.Addr.IsPrivate() doesn't cover it.
var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

type Proxy struct {
	allow  Allowlist
	logger *log.Logger
	dialer *net.Dialer

	// lookupIP resolves allowed domains for DNS queries.
	lookupIP func(ctx context.Context, host string) ([]netip.Addr, error)

	mu      sync.Mutex
	tunnels map[net.Conn]struct{}
	// resolved maps addresses returned by the DNS server to the names
	// that resolved to them.
	resolved map[netip.Addr]map[string]struct{}
	// closers are listeners closed when the proxy is closed.
	closers []io.Closer
	closed  bool
}

// New creates a proxy that allows connections to destinations
// matching allow. Each request is logged to logOut.
func New(allow Allowlist, logOut io.Writer) *Proxy {
	p := &Proxy{
		allow:    allow,
		logger:   log.New(logOut, "", log.LstdFlags),
		lookupIP: lookupIPv4,
		tunnels:  make(map[net.Conn]struct{}),
		resolved: make(map[netip.Addr]map[string]struct{}),
	}
	p.dialer = &net.Dialer{
		Timeout:        30 * time.Second,
		ControlContext: p.checkDialAddr,
	}
	return p
}

// Close stops the proxy and closes all the connections.
func (p *Proxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, c := range p.closers {
		c.Close()
	}
	for c := range p.tunnels {
		c.Close()
	}
}

// addCloser registers c to be closed when the proxy is closed.
// Returns false (and closes c) if the proxy is already closed.
func (p *Proxy) addCloser(c io.Closer) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		c.Close()
		return false
	}
	p.closers = append(p.closers, c)
	return true
}

func (p *Proxy) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// pipe copies data between the client and the upstream connection
// until both directions are finished, then closes the connections.
// Data from the client is read via clientReader.
func (p *Proxy) pipe(client net.Conn, clientReader io.Reader, upstream net.Conn) {
	defer client.Close()
	defer upstream.Close()
	if !p.trackTunnel(client, upstream) {
		return
	}
	defer p.untrackTunnel(client, upstream)

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(upstream, clientReader)
		closeWrite(upstream)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, upstream)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
}

// dialHost connects to address on behalf of the requested host,
// passing the host to checkDialAddr. The address can be the host
// itself or an IP address the host resolved to.
func (p *Proxy) dialHost(ctx context.Context, host, network, address string) (net.Conn, error) {
	ctx = context.WithValue(ctx, dialHostKey{}, normalizeHost(host))
	return p.dialer.DialContext(ctx, network, address)
}

// checkDialAddr is called for every address the dialer connects to,
// after DNS resolution. Checking the resolved address here, instead
// of resolving the domain separately before connecting, ensures that
// the checked address is the one used for the connection.
func (p *Proxy) checkDialAddr(ctx context.Context, network, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("parse dialed address %s: %v", address, err)
	}
	host, _ := ctx.Value(dialHostKey{}).(string)
	return p.checkAddr(host, addrPort)
}

// checkAddr checks if the requested host can be connected to via the
// resolved address.
//
// Allowed domains must not resolve to addresses of the host or local
// network services. Otherwise, a sandboxed process could use DNS to
// get access to these services. The exceptions are:
//   - IP addresses explicitly listed in allowed_domains,
//   - private (local network) addresses of domains listed exactly
//     (not via a wildcard) in allowed_domains, so local network
//     services can be allowed by their domain names. Loopback and
//     link-local addresses are never allowed for domains.
func (p *Proxy) checkAddr(host string, addrPort netip.AddrPort) error {
	addr := addrPort.Addr().Unmap()
	port := int(addrPort.Port())
	if isPublicAddr(addr) || p.allow.Allows(addr.String(), port) {
		return nil
	}
	if isPrivateAddr(addr) && host != "" && !isIPLiteral(host) &&
		p.allow.allowsExactDomain(host, port) {
		return nil
	}
	return fmt.Errorf("%w: %s", errForbiddenAddr, addr)
}

func isPublicAddr(addr netip.Addr) bool {
	return addr.IsGlobalUnicast() && !isPrivateAddr(addr)
}

// isPrivateAddr returns true for local network addresses.
func isPrivateAddr(addr netip.Addr) bool {
	return addr.IsPrivate() || cgnatPrefix.Contains(addr)
}

func (p *Proxy) trackTunnel(conns ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	for _, c := range conns {
		p.tunnels[c] = struct{}{}
	}
	return true
}

func (p *Proxy) untrackTunnel(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range conns {
		delete(p.tunnels, c)
	}
}

func closeWrite(c net.Conn) {
	if tc, ok := c.(interface{ CloseWrite() error }); ok {
		tc.CloseWrite()
	} else {
		c.Close()
	}
}
