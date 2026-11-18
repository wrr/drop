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
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// ipv4Packet returns an IPv4 packet to dst with a header of
// headerLen bytes (20 without options).
func ipv4Packet(headerLen int, proto byte, dst string, payload ...byte) []byte {
	p := make([]byte, max(headerLen, 20))
	p[0] = 0x40 | byte(headerLen/4)
	p[9] = proto
	addr := netip.MustParseAddr(dst).As4()
	copy(p[16:20], addr[:])
	return append(p, payload...)
}

func ipv6Packet(next byte, dst string, payload ...byte) []byte {
	p := make([]byte, 40)
	p[0] = 0x60
	p[6] = next
	addr := netip.MustParseAddr(dst).As16()
	copy(p[24:40], addr[:])
	return append(p, payload...)
}

func TestParseDestination(t *testing.T) {
	// Source port 12345, destination port 443.
	ports := []byte{0x30, 0x39, 0x01, 0xbb}
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	cases := []struct {
		name    string
		packet  []byte
		want    destination
		wantErr string
	}{
		{"ipv4 tcp", ipv4Packet(20, unix.IPPROTO_TCP, "192.0.2.1", ports...),
			destination{addr: v4, port: 443, proto: "tcp"}, ""},
		{"ipv4 udp with options", ipv4Packet(24, unix.IPPROTO_UDP, "192.0.2.1", ports...),
			destination{addr: v4, port: 443, proto: "udp"}, ""},
		{"ipv4 icmp", ipv4Packet(20, unix.IPPROTO_ICMP, "192.0.2.1", 8, 0, 0, 0),
			destination{addr: v4}, ""},
		{"ipv6 tcp", ipv6Packet(unix.IPPROTO_TCP, "2001:db8::1", ports...),
			destination{addr: v6, port: 443, proto: "tcp"}, ""},
		{"ipv6 icmp", ipv6Packet(unix.IPPROTO_ICMPV6, "2001:db8::1", 128, 0, 0, 0),
			destination{addr: v6}, ""},

		{"ipv4 header too short", ipv4Packet(16, unix.IPPROTO_TCP, "192.0.2.1", ports...),
			destination{}, "invalid IPv4 header length 16"},
		{"ipv4 header longer than packet", ipv4Packet(60, unix.IPPROTO_TCP, "192.0.2.1")[:40],
			destination{}, "invalid IPv4 header length 60"},
		{"ipv4 truncated tcp", ipv4Packet(20, unix.IPPROTO_TCP, "192.0.2.1", 0x30, 0x39, 0x01),
			destination{}, "truncated tcp header"},
		{"ipv6 truncated udp", ipv6Packet(unix.IPPROTO_UDP, "2001:db8::1"),
			destination{}, "truncated udp header"},
		{"ipv6 extension header", ipv6Packet(unix.IPPROTO_ROUTING, "2001:db8::1", ports...),
			destination{}, "unsupported IPv6 extension header 43"},
		{"not ip", append([]byte{0x50}, make([]byte, 39)...), destination{}, "unrecognized packet"},
		{"too short", []byte{0x45, 0, 0}, destination{}, "unrecognized packet"},
	}
	for _, c := range cases {
		got, err := parseDestination(c.packet)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.wantErr != "" && err == nil:
			t.Errorf("%s: expected error %q", c.name, c.wantErr)
		case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
			t.Errorf("%s: error %q, want %q", c.name, err, c.wantErr)
		}
		if got != c.want {
			t.Errorf("%s: got %s %s %d, want %s %s %d", c.name,
				got.addr, got.proto, got.port, c.want.addr, c.want.proto, c.want.port)
		}
	}
}
