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
	"bytes"
	"context"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"syscall"
	"testing"
)

// syncBuffer is a bytes.Buffer safe for concurrent use.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startEchoServer starts a TCP server that echoes back received data.
func startEchoServer(t *testing.T) netip.AddrPort {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				io.Copy(c, c)
				c.Close()
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).AddrPort()
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

func TestIsPublicAddr(t *testing.T) {
	tests := []struct {
		addr     string
		expected bool
	}{
		{"1.1.1.1", true},
		{"2606:4700::1111", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"0.0.0.0", false},
		{"10.1.2.3", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"100.100.1.1", false},
		{"fd00::1", false},
		{"fe80::1", false},
		{"224.0.0.1", false},
	}
	for _, tc := range tests {
		addr := netip.MustParseAddr(tc.addr)
		if got := isPublicAddr(addr); got != tc.expected {
			t.Errorf("isPublicAddr(%s) = %v, expected %v", tc.addr, got, tc.expected)
		}
	}
}

func TestCheckAddr(t *testing.T) {
	allow, err := ParseAllowlist([]string{
		"a.b.c",
		"*.lan.example",
		"api.example.com",
		"192.168.1.20:8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	p := New(allow, io.Discard)
	tests := []struct {
		host     string
		addr     string
		expected bool
	}{
		// Public addresses are always allowed.
		{"api.example.com", "93.184.215.14:443", true},
		// Exact domains can resolve to local network addresses.
		{"a.b.c", "192.168.1.10:443", true},
		{"a.b.c", "10.0.0.5:80", true},
		{"a.b.c", "100.100.1.1:443", true},
		{"a.b.c", "[fd00::1]:443", true},
		{"A.B.C.", "192.168.1.10:443", true},
		{"a.b.c", "192.168.1.10:22", false},
		// But never to loopback or link-local addresses.
		{"a.b.c", "127.0.0.1:443", false},
		{"a.b.c", "[::1]:443", false},
		{"a.b.c", "169.254.169.254:80", false},
		{"a.b.c", "[fe80::1]:443", false},
		// Wildcard domains can't resolve to local network addresses.
		{"x.lan.example", "192.168.1.10:443", false},
		{"x.lan.example", "93.184.215.14:443", true},
		// Explicitly listed IP addresses are allowed.
		{"192.168.1.20", "192.168.1.20:8080", true},
		{"a.b.c", "192.168.1.20:8080", true},
		{"192.168.1.10", "192.168.1.10:443", false},
		// Unknown requested host.
		{"", "192.168.1.10:443", false},
	}
	for _, tc := range tests {
		err := p.checkAddr(tc.host, netip.MustParseAddrPort(tc.addr))
		if got := err == nil; got != tc.expected {
			t.Errorf("checkAddr(%q, %s) allowed = %v, expected %v (err: %v)",
				tc.host, tc.addr, got, tc.expected, err)
		}
	}
}

func TestDialPassesRequestedHost(t *testing.T) {
	echo := startEchoServer(t)
	p := New(nil, io.Discard)
	var gotHost string
	p.dialer.ControlContext = func(ctx context.Context, network, address string, c syscall.RawConn) error {
		gotHost, _ = ctx.Value(dialHostKey{}).(string)
		return nil
	}
	conn, err := p.dialHost(context.Background(), "LocalHost.", "tcp", echo.String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if gotHost != "localhost" {
		t.Errorf("expected requested host 'localhost', got %q", gotHost)
	}
}
