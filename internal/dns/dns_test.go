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
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const readTimeout = 5 * time.Second

const (
	singleAName  = "singlea.test."
	multiAName   = "multia.test."
	nxdomainName = "nxdomain.test."
	nodataName   = "nodata.test."
	servfailName = "servfail.test."
	cnameName    = "cname.test."
	cnameTarget  = "target.test."
	manyAName    = "manya.test."
)

// Enough A records that the reply does not fit a 512 byte buffer even
// when compressed.
const manyACount = 40

var multiAddrs = []net.IP{
	net.IPv4(134, 12, 8, 7),
	net.IPv4(134, 12, 8, 8),
	net.IPv4(134, 12, 8, 9),
}

type fixture struct {
	dnsAddress      string
	answerA         net.IP
	upstreamQueries atomic.Int64
	upstreamName    atomic.Value
}

func (f *fixture) checkUpstreamQueries(t *testing.T, want int64) {
	t.Helper()
	if got := f.upstreamQueries.Load(); got != want {
		t.Errorf("upstream queries: got %d, want %d", got, want)
	}
}

func (f *fixture) checkUpstreamName(t *testing.T, want string) {
	t.Helper()
	got, _ := f.upstreamName.Load().(string)
	if got != want {
		t.Errorf("upstream name: got %s, want %s", got, want)
	}
}

func exchangeA(t *testing.T, dnsAddress, name string) *dns.A {
	t.Helper()
	query := new(dns.Msg)
	query.SetQuestion(name, dns.TypeA)
	reply, _, err := new(dns.Client).Exchange(query, dnsAddress)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if reply.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode: got %s, want NOERROR", dns.RcodeToString[reply.Rcode])
	}
	if !reply.RecursionAvailable {
		t.Error("ra bit not set")
	}
	if len(reply.Answer) != 1 {
		t.Fatalf("answer: got %v, want one A record", reply.Answer)
	}
	a, ok := reply.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("answer: got %s, want an A record", reply.Answer[0])
	}
	return a
}

func setupUpstream(t *testing.T, fxt *fixture) string {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	handle := func(w dns.ResponseWriter, req *dns.Msg) {
		fxt.upstreamQueries.Add(1)
		reply := new(dns.Msg)
		reply.SetReply(req)
		// Without the ra bit an empty NOERROR answer is discarded and the
		// next server is tried.
		reply.RecursionAvailable = true
		question := req.Question[0]
		fxt.upstreamName.Store(question.Name)
		switch {
		case question.Qtype != dns.TypeA:
			t.Errorf("A query expected, got: %s", dns.Type(question.Qtype))
			reply.Rcode = dns.RcodeRefused
		case question.Name == nxdomainName:
			reply.Rcode = dns.RcodeNameError
		case question.Name == servfailName:
			reply.Rcode = dns.RcodeServerFailure
		case question.Name == manyAName:
			reply.Compress = true
			for i := range manyACount {
				reply.Answer = append(reply.Answer, &dns.A{
					Hdr: header(question.Name, dns.TypeA),
					A:   net.IPv4(10, 0, 0, byte(i+1)),
				})
			}
		case question.Name == multiAName:
			for _, addr := range multiAddrs {
				reply.Answer = append(reply.Answer, &dns.A{
					Hdr: header(question.Name, dns.TypeA),
					A:   addr,
				})
			}
		case question.Name == cnameName:
			reply.Answer = []dns.RR{
				&dns.CNAME{
					Hdr:    header(cnameName, dns.TypeCNAME),
					Target: cnameTarget,
				},
				&dns.A{
					Hdr: header(cnameTarget, dns.TypeA),
					A:   fxt.answerA,
				},
			}
		case question.Name == nodataName:
			// NOERROR with no records
		case question.Name == singleAName:
			reply.Answer = []dns.RR{&dns.A{
				Hdr: header(question.Name, dns.TypeA),
				A:   fxt.answerA,
			}}
		default:
			t.Errorf("unexpected name: %s", question.Name)
			reply.Rcode = dns.RcodeRefused
		}
		if err := w.WriteMsg(reply); err != nil {
			t.Errorf("upstream send answer: %v", err)
		}
	}

	started := make(chan struct{})
	server := &dns.Server{
		PacketConn:        conn,
		Handler:           dns.HandlerFunc(handle),
		NotifyStartedFunc: func() { close(started) },
	}
	errs := make(chan error, 1)
	go func() { errs <- server.ActivateAndServe() }()
	select {
	case <-started:
	case err := <-errs:
		t.Fatalf("start upstream: %v", err)
	}
	t.Cleanup(func() { server.Shutdown() })

	return conn.LocalAddr().String()
}

var discardLogger = slog.New(slog.DiscardHandler)

func setupServer(t *testing.T, upstreamAddr string, logger *slog.Logger) string {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, upstreamAddr)
		},
	}
	stop, err := StartServer(conn, resolver, logger)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(stop)
	return conn.LocalAddr().String()
}

func setup(t *testing.T) *fixture {
	fxt := fixture{
		answerA: net.IPv4(134, 12, 8, 7),
	}
	upstreamAddr := setupUpstream(t, &fxt)
	fxt.dnsAddress = setupServer(t, upstreamAddr, discardLogger)
	return &fxt
}

func header(name string, rrtype uint16) dns.RR_Header {
	return dns.RR_Header{Name: name, Rrtype: rrtype, Class: dns.ClassINET, Ttl: 60}
}

func TestStartErrorReporting(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// passing already closed socket triggers an error
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	stop, err := StartServer(conn, &net.Resolver{}, discardLogger)
	if err == nil {
		stop()
		t.Fatal("start server: got no error for a closed conn")
	}
	if stop != nil {
		t.Error("start server: got a shutdown function along with an error")
	}
}

func TestARecordAnswered(t *testing.T) {
	fxt := setup(t)

	a := exchangeA(t, fxt.dnsAddress, singleAName)
	if !a.A.Equal(fxt.answerA) {
		t.Errorf("answer: got %s, want %s", a.A, fxt.answerA)
	}
	if a.Hdr.Ttl != ttl {
		t.Errorf("ttl: got %d, want %d", a.Hdr.Ttl, ttl)
	}
	fxt.checkUpstreamQueries(t, 1)
	fxt.checkUpstreamName(t, singleAName)
}

func TestMultipleQuestionsRejected(t *testing.T) {
	fxt := setup(t)

	query := new(dns.Msg)
	query.SetQuestion(singleAName, dns.TypeA)
	query.Question = append(query.Question, dns.Question{
		Name:   "other.example.com.",
		Qtype:  dns.TypeA,
		Qclass: dns.ClassINET,
	})
	reply, _, err := new(dns.Client).Exchange(query, fxt.dnsAddress)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	if reply.Rcode != dns.RcodeFormatError {
		t.Errorf("rcode: got %s, want FORMERR", dns.RcodeToString[reply.Rcode])
	}
	fxt.checkUpstreamQueries(t, 0)
}

func TestZeroQuestionsRejected(t *testing.T) {
	fxt := setup(t)

	query := new(dns.Msg)
	query.Id = dns.Id()
	reply, _, err := new(dns.Client).Exchange(query, fxt.dnsAddress)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	if reply.Rcode != dns.RcodeFormatError {
		t.Errorf("rcode: got %s, want FORMERR", dns.RcodeToString[reply.Rcode])
	}
	fxt.checkUpstreamQueries(t, 0)
}

func TestHeaderOnlyRequestRejected(t *testing.T) {
	fxt := setup(t)
	// A packet that declares one question in the header but has no
	// questions after the header is accepted by the server's accept
	// function. dns.Msg doesn't support building such requests.

	conn, err := net.Dial("udp4", fxt.dnsAddress)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	header := []byte{
		0x12, 0x34, // id
		0x01, 0x00, // query, recursion desired
		0x00, 0x01, // one question
		0x00, 0x00, // no answer records
		0x00, 0x00, // no authority records
		0x00, 0x00, // no additional records
	}
	if _, err := conn.Write(header); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	buf := make([]byte, dns.MinMsgSize)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	reply := new(dns.Msg)
	if err := reply.Unpack(buf[:n]); err != nil {
		t.Fatalf("unpack: %v", err)
	}

	if reply.Rcode != dns.RcodeFormatError {
		t.Errorf("rcode: got %s, want FORMERR", dns.RcodeToString[reply.Rcode])
	}
	if len(reply.Answer) != 0 {
		t.Errorf("answer: got %v, want no records", reply.Answer)
	}
	fxt.checkUpstreamQueries(t, 0)

	// The handler must not have taken the server down with it.
	exchangeA(t, fxt.dnsAddress, singleAName)
}

func TestNameCanonicalized(t *testing.T) {
	const name = "SinGleA.test."
	fxt := setup(t)

	a := exchangeA(t, fxt.dnsAddress, name)
	if a.Hdr.Name != name {
		t.Errorf("answer: got name %s, want %s", a.Hdr.Name, name)
	}
	fxt.checkUpstreamQueries(t, 1)
	fxt.checkUpstreamName(t, singleAName)
}

func TestRecursionNotDesiredRefused(t *testing.T) {
	fxt := setup(t)

	query := new(dns.Msg)
	query.SetQuestion(singleAName, dns.TypeA)
	query.RecursionDesired = false
	reply, _, err := new(dns.Client).Exchange(query, fxt.dnsAddress)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	if reply.Rcode != dns.RcodeRefused {
		t.Errorf("rcode: got %s, want REFUSED", dns.RcodeToString[reply.Rcode])
	}
	if len(reply.Answer) != 0 {
		t.Errorf("answer: got %v, want no records", reply.Answer)
	}
	fxt.checkUpstreamQueries(t, 0)
}

func TestNonInternetClassRefused(t *testing.T) {
	fxt := setup(t)

	query := new(dns.Msg)
	query.SetQuestion(singleAName, dns.TypeA)
	query.Question[0].Qclass = dns.ClassCHAOS
	reply, _, err := new(dns.Client).Exchange(query, fxt.dnsAddress)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	if reply.Rcode != dns.RcodeRefused {
		t.Errorf("rcode: got %s, want REFUSED", dns.RcodeToString[reply.Rcode])
	}
	if len(reply.Answer) != 0 {
		t.Errorf("answer: got %v, want no records", reply.Answer)
	}
	fxt.checkUpstreamQueries(t, 0)
}

func TestNoDataReturned(t *testing.T) {
	fxt := setup(t)

	qtypes := []uint16{
		dns.TypeAAAA, dns.TypeSVCB, dns.TypeHTTPS,
		dns.TypeMX, dns.TypeTXT, dns.TypePTR, dns.TypeSRV, dns.TypeANY,
	}
	for _, qtype := range qtypes {
		t.Run(dns.Type(qtype).String(), func(t *testing.T) {
			query := new(dns.Msg)
			query.SetQuestion(singleAName, qtype)
			reply, _, err := new(dns.Client).Exchange(query, fxt.dnsAddress)
			if err != nil {
				t.Fatalf("exchange: %v", err)
			}

			if reply.Rcode != dns.RcodeSuccess {
				t.Errorf("rcode: got %s, want NOERROR", dns.RcodeToString[reply.Rcode])
			}
			if len(reply.Answer) != 0 {
				t.Errorf("answer: got %v, want no records", reply.Answer)
			}
			if !reply.RecursionAvailable {
				t.Error("ra bit not set")
			}
		})
	}
	fxt.checkUpstreamQueries(t, 0)
}

func TestFailedLookupsReported(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
	}{
		{nxdomainName, dns.RcodeNameError},
		{nodataName, dns.RcodeNameError},
		{servfailName, dns.RcodeServerFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fxt := setup(t)

			query := new(dns.Msg)
			query.SetQuestion(tc.name, dns.TypeA)
			reply, _, err := new(dns.Client).Exchange(query, fxt.dnsAddress)
			if err != nil {
				t.Fatalf("exchange: %v", err)
			}

			if reply.Rcode != tc.want {
				t.Errorf("rcode: got %s, want %s",
					dns.RcodeToString[reply.Rcode], dns.RcodeToString[tc.want])
			}
			if len(reply.Answer) != 0 {
				t.Errorf("answer: got %v, want no records", reply.Answer)
			}
			// RcodeServerFailure can be retried multiple times.
			if queries := fxt.upstreamQueries.Load(); queries < 1 {
				t.Errorf("upstream queries: got %d, want at least 1", queries)
			}
		})
	}
}

func TestCnameFlattened(t *testing.T) {
	fxt := setup(t)

	a := exchangeA(t, fxt.dnsAddress, cnameName)
	if a.Hdr.Name != cnameName {
		t.Errorf("answer: got name %s, want %s", a.Hdr.Name, cnameName)
	}
	if !a.A.Equal(fxt.answerA) {
		t.Errorf("answer: got %s, want %s", a.A, fxt.answerA)
	}
	fxt.checkUpstreamQueries(t, 1)
}

// A client that sends no OPT record reads at most 512 bytes, so a
// larger reply has to lose records and report it with the truncated bit.
func TestLargeReplyTruncated(t *testing.T) {
	fxt := setup(t)

	query := new(dns.Msg)
	query.SetQuestion(manyAName, dns.TypeA)
	reply, _, err := new(dns.Client).Exchange(query, fxt.dnsAddress)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	if reply.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode: got %s, want NOERROR", dns.RcodeToString[reply.Rcode])
	}
	if !reply.Truncated {
		t.Error("tc bit not set")
	}
	if len(reply.Answer) == 0 || len(reply.Answer) >= manyACount {
		t.Errorf("answer: got %d records, want some but fewer than %d",
			len(reply.Answer), manyACount)
	}
	if opt := reply.IsEdns0(); opt != nil {
		t.Errorf("opt record in reply to a query without one: %s", opt)
	}
	fxt.checkUpstreamQueries(t, 1)
}

// A client that advertises a larger buffer gets the whole reply and an
// OPT record of its own.
func TestLargeReplyFitsAdvertisedSize(t *testing.T) {
	fxt := setup(t)

	query := new(dns.Msg)
	query.SetQuestion(manyAName, dns.TypeA)
	query.SetEdns0(8000, false)
	reply, _, err := (&dns.Client{UDPSize: 8000}).Exchange(query, fxt.dnsAddress)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	if reply.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode: got %s, want NOERROR", dns.RcodeToString[reply.Rcode])
	}
	if reply.Truncated {
		t.Error("tc bit set")
	}
	if len(reply.Answer) != manyACount {
		t.Errorf("answer: got %d records, want %d", len(reply.Answer), manyACount)
	}
	opt := reply.IsEdns0()
	if opt == nil {
		t.Fatal("no opt record in reply to an edns0 query")
	}
	if got := opt.UDPSize(); got != 512 {
		t.Errorf("advertised udp size: got %d, want %d", got, 512)
	}
	if opt.Do() {
		t.Error("do bit set")
	}
	fxt.checkUpstreamQueries(t, 1)
}

func TestSeveralARecordsAnswered(t *testing.T) {
	fxt := setup(t)

	query := new(dns.Msg)
	query.SetQuestion(multiAName, dns.TypeA)
	reply, _, err := new(dns.Client).Exchange(query, fxt.dnsAddress)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if reply.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode: got %s, want NOERROR", dns.RcodeToString[reply.Rcode])
	}
	var got []string
	for _, rr := range reply.Answer {
		a, ok := rr.(*dns.A)
		if !ok {
			t.Fatalf("answer: got %s, want an A record", rr)
		}
		if a.Hdr.Name != multiAName {
			t.Errorf("answer: got name %s, want %s", a.Hdr.Name, multiAName)
		}
		got = append(got, a.A.String())
	}
	var want []string
	for _, addr := range multiAddrs {
		want = append(want, addr.String())
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("answer: got %v, want %v", got, want)
	}
	fxt.checkUpstreamQueries(t, 1)
}

// lineWriter passes each log line to a channel, so a test can wait for
// a line the server logs after sending the reply.
type lineWriter chan string

func (w lineWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func TestRefusedQueryLogged(t *testing.T) {
	fxt := fixture{answerA: net.IPv4(134, 12, 8, 7)}
	lines := make(lineWriter, 10)
	logger := slog.New(slog.NewTextHandler(lines, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fxt.dnsAddress = setupServer(t, setupUpstream(t, &fxt), logger)

	tests := []struct {
		name   string
		modify func(q *dns.Msg)
		want   []string
	}{
		{
			name:   "recursion not desired",
			modify: func(q *dns.Msg) { q.RecursionDesired = false },
			want: []string{"level=DEBUG",
				`msg="dns query refused: recursion not desired"`, "type=A",
				"name=" + singleAName},
		},
		{
			name:   "non-IN class",
			modify: func(q *dns.Msg) { q.Question[0].Qclass = dns.ClassCHAOS },
			want: []string{"level=DEBUG", `msg="dns query refused: CH class"`,
				"name=" + singleAName},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := new(dns.Msg)
			query.SetQuestion(singleAName, dns.TypeA)
			tt.modify(query)
			if _, _, err := new(dns.Client).Exchange(query, fxt.dnsAddress); err != nil {
				t.Fatalf("exchange: %v", err)
			}
			select {
			case line := <-lines:
				for _, w := range tt.want {
					if !strings.Contains(line, w) {
						t.Errorf("log line %q does not contain %q", line, w)
					}
				}
			case <-time.After(readTimeout):
				t.Fatal("no log line")
			}
		})
	}
}
