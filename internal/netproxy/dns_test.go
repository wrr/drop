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
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	dnsTypeAAAA = 28
	testQueryID = 0x1234
)

// buildDNSQuery builds a query with a single question.
func buildDNSQuery(name string, qtype uint16) []byte {
	msg := binary.BigEndian.AppendUint16(nil, testQueryID)
	msg = binary.BigEndian.AppendUint16(msg, dnsFlagRecursionDesired)
	msg = binary.BigEndian.AppendUint16(msg, 1)
	msg = append(msg, 0, 0, 0, 0, 0, 0)
	for _, label := range strings.Split(name, ".") {
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0)
	msg = binary.BigEndian.AppendUint16(msg, qtype)
	msg = binary.BigEndian.AppendUint16(msg, dnsClassIN)
	return msg
}

type dnsResult struct {
	id    uint16
	rcode int
	addrs []netip.Addr
}

// parseDNSResponse parses a response built by dnsResponse.
func parseDNSResponse(t *testing.T, query, resp []byte) dnsResult {
	t.Helper()
	if len(resp) < dnsHeaderLen {
		t.Fatalf("response too short: %v", resp)
	}
	flags := binary.BigEndian.Uint16(resp[2:])
	if flags&dnsFlagResponse == 0 {
		t.Fatalf("response flag not set")
	}
	result := dnsResult{
		id:    binary.BigEndian.Uint16(resp[0:]),
		rcode: int(flags & 0xf),
	}
	qdCount := binary.BigEndian.Uint16(resp[4:])
	anCount := int(binary.BigEndian.Uint16(resp[6:]))
	pos := dnsHeaderLen
	if qdCount == 1 {
		question := query[dnsHeaderLen:]
		if !bytes.Equal(resp[pos:pos+len(question)], question) {
			t.Fatalf("question not echoed")
		}
		pos += len(question)
	}
	for range anCount {
		// name pointer, type, class, ttl, rdlength
		rdLen := int(binary.BigEndian.Uint16(resp[pos+10:]))
		if rdLen != 4 {
			t.Fatalf("unexpected rdlength %d", rdLen)
		}
		addr, _ := netip.AddrFromSlice(resp[pos+12 : pos+16])
		result.addrs = append(result.addrs, addr)
		pos += 16
	}
	if pos != len(resp) {
		t.Fatalf("trailing data in response")
	}
	return result
}

func newDNSTestProxy(t *testing.T, entries ...string) *Proxy {
	t.Helper()
	allow, err := ParseAllowlist(entries)
	if err != nil {
		t.Fatal(err)
	}
	p := New(allow, io.Discard)
	p.lookupIP = func(ctx context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "example.com", "api.example.org":
			return []netip.Addr{
				netip.MustParseAddr("93.184.215.14"),
				netip.MustParseAddr("2606:2800:21f::1"),
				netip.MustParseAddr("::ffff:93.184.215.15"),
			}, nil
		case "missing.example.org":
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		return nil, &net.DNSError{Err: "server misbehaving", Name: host}
	}
	return p
}

func TestHandleDNS(t *testing.T) {
	p := newDNSTestProxy(t, "example.com", "*.example.org")
	example := []netip.Addr{
		netip.MustParseAddr("93.184.215.14"),
		netip.MustParseAddr("93.184.215.15"),
	}
	tests := []struct {
		name     string
		qtype    uint16
		rcode    int
		expected []netip.Addr
	}{
		{"example.com", dnsTypeA, dnsRcodeNoError, example},
		{"EXAMPLE.com", dnsTypeA, dnsRcodeNoError, example},
		{"api.example.org", dnsTypeA, dnsRcodeNoError, example},
		{"example.com", dnsTypeAAAA, dnsRcodeNoError, nil},
		{"missing.example.org", dnsTypeA, dnsRcodeNXDomain, nil},
		{"broken.example.org", dnsTypeA, dnsRcodeServFail, nil},
		{"example.org", dnsTypeA, dnsRcodeNXDomain, nil},
		{"evil.com", dnsTypeA, dnsRcodeNXDomain, nil},
		{"www.example.com", dnsTypeA, dnsRcodeNXDomain, nil},
	}
	for _, tc := range tests {
		query := buildDNSQuery(tc.name, tc.qtype)
		result := parseDNSResponse(t, query, p.handleDNS(query))
		if result.id != testQueryID {
			t.Errorf("%s: unexpected id %x", tc.name, result.id)
		}
		if result.rcode != tc.rcode {
			t.Errorf("%s type %d: rcode %d, expected %d", tc.name, tc.qtype, result.rcode, tc.rcode)
		}
		if !slices.Equal(result.addrs, tc.expected) {
			t.Errorf("%s type %d: addrs %v, expected %v", tc.name, tc.qtype, result.addrs, tc.expected)
		}
	}

	names := p.resolvedNames(netip.MustParseAddr("93.184.215.14"))
	if !slices.Equal(names, []string{"api.example.org", "example.com"}) {
		t.Errorf("unexpected resolved names %v", names)
	}
	if names := p.resolvedNames(netip.MustParseAddr("192.0.2.1")); len(names) != 0 {
		t.Errorf("unexpected resolved names %v", names)
	}
}

func TestHandleDNSInvalid(t *testing.T) {
	p := newDNSTestProxy(t, "example.com")

	if resp := p.handleDNS([]byte{1, 2, 3}); resp != nil {
		t.Errorf("expected no response to a too short message")
	}

	response := buildDNSQuery("example.com", dnsTypeA)
	binary.BigEndian.PutUint16(response[2:], dnsFlagResponse)
	if resp := p.handleDNS(response); resp != nil {
		t.Errorf("expected no response to a response")
	}

	truncated := buildDNSQuery("example.com", dnsTypeA)
	truncated = truncated[:len(truncated)-3]
	result := parseDNSResponse(t, nil, p.handleDNS(truncated))
	if result.rcode != dnsRcodeFormErr {
		t.Errorf("truncated query: rcode %d, expected FORMERR", result.rcode)
	}

	// A label with a dot must not match "example.com".
	dotted := buildDNSQuery("x", dnsTypeA)
	dotted = slices.Concat(dotted[:dnsHeaderLen], []byte("\x0bexample.com\x00"), dotted[len(dotted)-4:])
	result = parseDNSResponse(t, nil, p.handleDNS(dotted))
	if result.rcode != dnsRcodeFormErr {
		t.Errorf("dotted label: rcode %d, expected FORMERR", result.rcode)
	}

	inverse := buildDNSQuery("example.com", dnsTypeA)
	binary.BigEndian.PutUint16(inverse[2:], 1<<11)
	result = parseDNSResponse(t, inverse, p.handleDNS(inverse))
	if result.rcode != dnsRcodeNotImp {
		t.Errorf("inverse query: rcode %d, expected NOTIMP", result.rcode)
	}
}

func TestServeDNS(t *testing.T) {
	p := newDNSTestProxy(t, "example.com")
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go p.ServeDNS(pc)
	defer p.Close()

	conn, err := net.Dial("udp4", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query := buildDNSQuery("example.com", dnsTypeA)
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	result := parseDNSResponse(t, query, buf[:n])
	if result.rcode != dnsRcodeNoError || len(result.addrs) != 2 {
		t.Errorf("unexpected response %+v", result)
	}
}
