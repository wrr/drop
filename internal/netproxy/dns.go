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
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// Minimal DNS server (RFC 1035) for sandboxed processes. Only allowed
// domains are resolved, other names get NXDOMAIN response. Addresses
// returned for allowed domains are recorded, so connections to these
// addresses can be matched to the allowed domain (see transparent.go).
//
// Only IPv4 addresses (A records) are returned, the sandbox has no
// IPv6 connectivity.

const (
	dnsHeaderLen = 12

	dnsFlagResponse         = 0x8000
	dnsFlagRecursionDesired = 0x0100
	dnsFlagRecursionAvail   = 0x0080
	dnsOpcodeMask           = 0x7800

	dnsTypeA   = 1
	dnsClassIN = 1

	dnsRcodeNoError  = 0
	dnsRcodeFormErr  = 1
	dnsRcodeServFail = 2
	dnsRcodeNXDomain = 3
	dnsRcodeNotImp   = 4

	dnsAnswerTTL  = 60
	maxDNSAnswers = 16
	dnsTimeout    = 10 * time.Second
)

type dnsQuery struct {
	id    uint16
	flags uint16
	// name is lower-case, without the trailing dot.
	name   string
	qtype  uint16
	qclass uint16
	// question is the question section as received.
	question []byte
}

// parseDNSQuery parses a DNS query with a single question.
func parseDNSQuery(msg []byte) (*dnsQuery, error) {
	if len(msg) < dnsHeaderLen {
		return nil, errors.New("message too short")
	}
	q := &dnsQuery{
		id:    binary.BigEndian.Uint16(msg[0:]),
		flags: binary.BigEndian.Uint16(msg[2:]),
	}
	if qdCount := binary.BigEndian.Uint16(msg[4:]); qdCount != 1 {
		return q, fmt.Errorf("expected 1 question, got %d", qdCount)
	}
	var labels []string
	pos := dnsHeaderLen
	for {
		if pos >= len(msg) {
			return q, errors.New("truncated name")
		}
		labelLen := int(msg[pos])
		pos++
		if labelLen == 0 {
			break
		}
		// Compression pointers are not used in questions of a query.
		if labelLen > 63 || pos+labelLen > len(msg) {
			return q, errors.New("invalid label")
		}
		label := string(msg[pos : pos+labelLen])
		if strings.Contains(label, ".") {
			return q, errors.New("invalid label")
		}
		labels = append(labels, strings.ToLower(label))
		pos += labelLen
	}
	if pos+4 > len(msg) {
		return q, errors.New("truncated question")
	}
	q.name = strings.Join(labels, ".")
	if len(q.name) > 253 {
		return q, errors.New("name too long")
	}
	q.qtype = binary.BigEndian.Uint16(msg[pos:])
	q.qclass = binary.BigEndian.Uint16(msg[pos+2:])
	q.question = msg[dnsHeaderLen : pos+4]
	return q, nil
}

// dnsResponse builds a response to the query with A records for addrs.
func dnsResponse(q *dnsQuery, rcode int, addrs []netip.Addr) []byte {
	flags := dnsFlagResponse | dnsFlagRecursionAvail |
		q.flags&(dnsOpcodeMask|dnsFlagRecursionDesired) | uint16(rcode)
	qdCount := 0
	if q.question != nil {
		qdCount = 1
	}
	msg := binary.BigEndian.AppendUint16(nil, q.id)
	msg = binary.BigEndian.AppendUint16(msg, flags)
	msg = binary.BigEndian.AppendUint16(msg, uint16(qdCount))
	msg = binary.BigEndian.AppendUint16(msg, uint16(len(addrs)))
	msg = binary.BigEndian.AppendUint16(msg, 0)
	msg = binary.BigEndian.AppendUint16(msg, 0)
	msg = append(msg, q.question...)
	for _, addr := range addrs {
		// Pointer to the name in the question.
		msg = binary.BigEndian.AppendUint16(msg, 0xc000|dnsHeaderLen)
		msg = binary.BigEndian.AppendUint16(msg, dnsTypeA)
		msg = binary.BigEndian.AppendUint16(msg, dnsClassIN)
		msg = binary.BigEndian.AppendUint32(msg, dnsAnswerTTL)
		ip := addr.As4()
		msg = binary.BigEndian.AppendUint16(msg, uint16(len(ip)))
		msg = append(msg, ip[:]...)
	}
	return msg
}

// ServeDNS answers DNS queries received on pc until Close is called.
func (p *Proxy) ServeDNS(pc net.PacketConn) error {
	if !p.addCloser(pc) {
		return nil
	}
	buf := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if p.isClosed() {
				return nil
			}
			return err
		}
		msg := slices.Clone(buf[:n])
		go func() {
			if resp := p.handleDNS(msg); resp != nil {
				pc.WriteTo(resp, from)
			}
		}()
	}
}

// handleDNS returns a response to the DNS query msg, or nil if no
// response should be sent.
func (p *Proxy) handleDNS(msg []byte) []byte {
	q, err := parseDNSQuery(msg)
	if q == nil {
		return nil
	}
	if q.flags&dnsFlagResponse != 0 {
		return nil
	}
	if err != nil {
		q.question = nil
		return dnsResponse(q, dnsRcodeFormErr, nil)
	}
	if q.flags&dnsOpcodeMask != 0 || q.qclass != dnsClassIN {
		return dnsResponse(q, dnsRcodeNotImp, nil)
	}
	if !p.allow.allowsName(q.name) {
		p.logger.Printf("denied DNS %s: not in net.allowed_domains", q.name)
		return dnsResponse(q, dnsRcodeNXDomain, nil)
	}
	if q.qtype != dnsTypeA {
		// The name exists, but only A records are returned.
		return dnsResponse(q, dnsRcodeNoError, nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dnsTimeout)
	defer cancel()
	addrs, err := p.lookupIP(ctx, q.name)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return dnsResponse(q, dnsRcodeNXDomain, nil)
		}
		p.logger.Printf("failed DNS %s: %v", q.name, err)
		return dnsResponse(q, dnsRcodeServFail, nil)
	}
	var ipv4 []netip.Addr
	for _, addr := range addrs {
		if addr = addr.Unmap(); addr.Is4() && len(ipv4) < maxDNSAnswers {
			ipv4 = append(ipv4, addr)
		}
	}
	p.recordResolved(q.name, ipv4)
	return dnsResponse(q, dnsRcodeNoError, ipv4)
}

func lookupIPv4(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
}

// recordResolved records that name resolved to addrs.
func (p *Proxy) recordResolved(name string, addrs []netip.Addr) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, addr := range addrs {
		names := p.resolved[addr]
		if names == nil {
			names = make(map[string]struct{})
			p.resolved[addr] = names
		}
		names[name] = struct{}{}
	}
}

// resolvedNames returns names that resolved to addr, sorted.
func (p *Proxy) resolvedNames(addr netip.Addr) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var names []string
	for name := range p.resolved[addr] {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
