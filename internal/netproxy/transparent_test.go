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
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPorts(t *testing.T) {
	allow, err := ParseAllowlist([]string{"a.com", "b.com:22", "c.com:443", "*.d.com:8080", "192.0.2.1:22"})
	if err != nil {
		t.Fatal(err)
	}
	if ports := allow.Ports(); !slices.Equal(ports, []int{22, 80, 443, 8080}) {
		t.Errorf("unexpected ports %v", ports)
	}
	if ports := Allowlist(nil).Ports(); len(ports) != 0 {
		t.Errorf("unexpected ports %v", ports)
	}
}

func TestTransparentHost(t *testing.T) {
	allow, err := ParseAllowlist([]string{"a.com", "*.b.com", "ssh.c.com:22", "192.0.2.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	p := New(allow, io.Discard)
	addrA := netip.MustParseAddr("198.51.100.1")
	addrB := netip.MustParseAddr("198.51.100.2")
	addrC := netip.MustParseAddr("198.51.100.3")
	p.recordResolved("a.com", []netip.Addr{addrA})
	p.recordResolved("x.b.com", []netip.Addr{addrB})
	p.recordResolved("ssh.c.com", []netip.Addr{addrC})
	// A name that is no longer allowed (not possible in practice).
	p.recordResolved("evil.com", []netip.Addr{addrC})

	tests := []struct {
		addr     netip.Addr
		port     int
		expected string
	}{
		{addrA, 443, "a.com"},
		{addrA, 80, "a.com"},
		{addrA, 22, ""},
		{addrB, 443, "x.b.com"},
		{addrC, 22, "ssh.c.com"},
		{addrC, 443, ""},
		{netip.MustParseAddr("192.0.2.1"), 8080, "192.0.2.1"},
		{netip.MustParseAddr("192.0.2.1"), 443, ""},
		{netip.MustParseAddr("203.0.113.9"), 443, ""},
	}
	for _, tc := range tests {
		host, ok := p.transparentHost(tc.addr, tc.port)
		if host != tc.expected || ok != (tc.expected != "") {
			t.Errorf("transparentHost(%s, %d) = %q, %v, expected %q",
				tc.addr, tc.port, host, ok, tc.expected)
		}
	}
}

// interceptedConn returns the client end of a connection, for which
// the server end is handled by the proxy as intercepted connection to
// dst.
func interceptedConn(t *testing.T, p *Proxy, dst netip.AddrPort) net.Conn {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	client, err := net.Dial("tcp4", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	go p.handleTransparent(server, dst)
	return client
}

func TestHandleTransparent(t *testing.T) {
	echo := startEchoServer(t)
	port := echo.Port()
	allow, err := ParseAllowlist([]string{"127.0.0.1:" + itoa(int(port))})
	if err != nil {
		t.Fatal(err)
	}
	var logBuf syncBuffer
	p := New(allow, &logBuf)
	defer p.Close()

	// Allowed explicitly listed address.
	client := interceptedConn(t, p, echo)
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(client, buf); err != nil || string(buf) != "hello" {
		t.Errorf("unexpected echo %q, err %v", buf, err)
	}

	// Address not resolved for any allowed domain.
	client = interceptedConn(t, p, netip.MustParseAddrPort("203.0.113.9:443"))
	expectClosed(t, client)

	// Allowed domain that resolved to loopback.
	loopAllow, err := ParseAllowlist([]string{"loop.example.com:" + itoa(int(port))})
	if err != nil {
		t.Fatal(err)
	}
	loopProxy := New(loopAllow, &logBuf)
	defer loopProxy.Close()
	loopProxy.recordResolved("loop.example.com", []netip.Addr{echo.Addr()})
	client = interceptedConn(t, loopProxy, echo)
	expectClosed(t, client)

	log := logBuf.String()
	for _, expected := range []string{
		"allowed TCP 127.0.0.1 (" + echo.String() + ")",
		"denied TCP 203.0.113.9:443: no allowed domain",
		"denied TCP loop.example.com (" + echo.String() + "): dial tcp " +
			echo.String() + ": " + errForbiddenAddr.Error(),
	} {
		if !strings.Contains(log, expected) {
			t.Errorf("log doesn't contain %q:\n%s", expected, log)
		}
	}
}

func expectClosed(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, syscall.ECONNRESET) {
		t.Errorf("expected connection reset, got %v", err)
	}
}

func TestServeTransparentClose(t *testing.T) {
	p := New(nil, io.Discard)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.ServeTransparent(l) }()
	// Accepted connection to an address that is not allowed. The reset
	// can be reported already by Dial.
	c, err := net.Dial("tcp4", l.Addr().String())
	if err == nil {
		expectClosed(t, c)
	} else if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatal(err)
	}
	p.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeTransparent didn't return after Close")
	}
}
