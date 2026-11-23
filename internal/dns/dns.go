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

package dns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"

	"github.com/miekg/dns" // Until v2 is stable

	"github.com/wrr/drop/internal/netns"
)

const ttl = 5

// Resolver resolves names for the server.
//
// nxdomain must be reported with a *net.DNSError with IsNotFound set,
// other errors are answered with SERVFAIL.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type server struct {
	resolver Resolver
	logger   *slog.Logger
}

// ListenLoopbackUDP returns a UDP socket bound to 127.0.0.1:port in
// the network namespace of the process pid.
//
// The main thread of the process must be locked to the main goroutine.
func ListenLoopbackUDP(pid int, port uint16) (net.PacketConn, error) {
	addr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)
	var conn *net.UDPConn
	err := netns.Do(pid, func() error {
		var err error
		conn, err = net.ListenUDP("udp4", net.UDPAddrFromAddrPort(addr))
		if err != nil {
			return fmt.Errorf("listen on %v in network namespace: %v", addr, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// StartServer answers queries arriving on conn until the returned
// shutdown function is called. Names are resolved with resolver.
//
// Takes ownership of conn, closing it when the server fails to start
// or the shutdown function is called.
func StartServer(conn net.PacketConn, resolver Resolver, logger *slog.Logger) (func(), error) {
	s := &server{resolver: resolver, logger: logger}
	srv := &dns.Server{
		PacketConn: conn,
		Handler:    dns.HandlerFunc(s.handle),
	}

	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	errs := make(chan error, 1)
	go func() { errs <- srv.ActivateAndServe() }()
	select {
	case <-started:
	case err := <-errs:
		conn.Close()
		return nil, fmt.Errorf("start DNS server: %v", err)
	}

	return func() { srv.Shutdown() }, nil
}

func (s *server) handle(w dns.ResponseWriter, req *dns.Msg) {
	reply := new(dns.Msg)
	reply.SetReply(req)
	reply.RecursionAvailable = true

	if len(req.Question) != 1 {
		// Accept only allows requests with question count set to 1 in the
		// header, but it allows requests that end just after the header,
		// without including any questions.
		s.logger.Debug("dns query without question")
		reply.Rcode = dns.RcodeFormatError
		s.send(w, reply)
		return
	}
	question := req.Question[0]
	qtype := dns.TypeToString[question.Qtype]

	switch {
	case !req.RecursionDesired:
		// The host resolver always recurses.
		s.logger.Debug("dns query refused: recursion not desired",
			"type", qtype, "name", question.Name)
		reply.Rcode = dns.RcodeRefused
	case question.Qclass != dns.ClassINET:
		// IN is the only class in practical use.
		s.logger.Debug("dns query refused: "+dns.Class(question.Qclass).String()+" class",
			"type", qtype, "name", question.Name)
		reply.Rcode = dns.RcodeRefused
	case question.Qtype == dns.TypeA:
		s.resolve(reply, question)
	case question.Qtype == dns.TypeAAAA:
		// No IPv6 support with firewall yet. An empty NOERROR keeps
		// the client on A, REFUSED would make it fail the lookup.
		s.logger.Debug("dns query not answered: no IPv6 support",
			"type", qtype, "name", question.Name)
	default:
		// Also an empty NOERROR: clients fall back to A or give up,
		// REFUSED can be treated as a server problem and
		// can lead to retriers.
		s.logger.Debug("dns query not answered: unsupported type",
			"type", qtype, "name", question.Name)
	}

	minClientSize := dns.MinMsgSize
	if opt := req.IsEdns0(); opt != nil {
		// A reply must carry an OPT record if the query did.

		// We just advertise the default UDP max size of 512, because we
		// expect just a single query, but any larger size will work
		// without fragmentation, as this is a local communication only.
		reply.SetEdns0(dns.MinMsgSize, false)
		minClientSize = int(opt.UDPSize())
	}
	// Drops records that do not fit the client buffer and sets the
	// truncated bit. Enables compression when needed.
	reply.Truncate(minClientSize)
	s.send(w, reply)
}

func (s *server) send(w dns.ResponseWriter, reply *dns.Msg) {
	if err := w.WriteMsg(reply); err != nil {
		s.logger.Error("send dns reply", "err", err)
	}
}

// resolve fills in reply for an address query.
func (s *server) resolve(reply *dns.Msg, question dns.Question) {
	// No explicit timeout: use timeout from the host resolv.conf or the
	// default.
	ctx := context.Background()

	// The incoming name is always rooted, CanonicalName ensures it is
	// also lowercase, so the firewall always sees rooted, lowercase
	// names.
	addrs, err := s.resolver.LookupNetIP(ctx, "ip4", dns.CanonicalName(question.Name))
	if err != nil {
		var dnsErr *net.DNSError
		switch {
		case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
			reply.Rcode = dns.RcodeNameError
		default:
			reply.Rcode = dns.RcodeServerFailure
		}
		return
	}

	header := dns.RR_Header{
		Name:   question.Name,
		Rrtype: question.Qtype,
		Class:  dns.ClassINET,
		Ttl:    ttl,
	}
	for _, addr := range addrs {
		addr = addr.Unmap()
		if !addr.Is4() {
			// We support ipv4 only
			continue
		}
		reply.Answer = append(reply.Answer, &dns.A{Hdr: header, A: addr.AsSlice()})
	}
}
