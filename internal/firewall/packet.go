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
	"encoding/binary"
	"fmt"
	"net/netip"

	"golang.org/x/sys/unix"
)

func parseDestination(packet []byte) (destination, error) {
	var d destination
	var proto byte
	var transport []byte
	switch {
	case isIPv4(packet):
		d.addr = netip.AddrFrom4([4]byte(packet[16:20]))
		proto = packet[9]
		headerLen := int(packet[0]&0x0f) * 4
		if headerLen < 20 || headerLen > len(packet) {
			return destination{}, fmt.Errorf("invalid IPv4 header length %d", headerLen)
		}
		transport = packet[headerLen:]
	case isIPv6(packet):
		if hasIPv6ExtensionHeader(packet) {
			// Not followed, so the transport header and, with a routing
			// header, the final destination are unknown.
			return destination{}, fmt.Errorf("unsupported IPv6 extension header %d", packet[6])
		}
		d.addr = netip.AddrFrom16([16]byte(packet[24:40]))
		proto = packet[6]
		transport = packet[40:]
	default:
		return destination{}, fmt.Errorf("unrecognized packet of %d bytes", len(packet))
	}

	d.proto = protoName(proto)
	if d.proto == "" {
		return d, nil
	}
	if len(transport) < 4 {
		return destination{}, fmt.Errorf("truncated %s header", d.proto)
	}
	d.port = int(binary.BigEndian.Uint16(transport[2:4]))
	return d, nil
}

// hasIPv6ExtensionHeader reports whether the IPv6 packet's next header
// is an extension header (RFC 8200 and the IANA IPv6 Extension Header
// Types).
func hasIPv6ExtensionHeader(packet []byte) bool {
	switch packet[6] {
	case unix.IPPROTO_HOPOPTS,
		unix.IPPROTO_ROUTING,
		unix.IPPROTO_FRAGMENT,
		unix.IPPROTO_ESP,
		unix.IPPROTO_AH,
		unix.IPPROTO_DSTOPTS,
		unix.IPPROTO_MH,
		139,      // Host Identity Protocol
		140,      // Shim6
		253, 254: // Experimentation and testing
		return true
	}
	return false
}

func isIPv4(packet []byte) bool {
	return len(packet) >= 20 && packet[0]>>4 == 4
}

func isIPv6(packet []byte) bool {
	return len(packet) >= 40 && packet[0]>>4 == 6
}

func protoName(proto byte) string {
	switch proto {
	case unix.IPPROTO_TCP:
		return "tcp"
	case unix.IPPROTO_UDP:
		return "udp"
	}
	return ""
}
