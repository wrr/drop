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

package netns

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/netproxy"
)

// Sockets created by ListenFiltered, in the order they are returned.
const (
	dnsSocket = iota
	// Followed by sockets for intercepted TCP connections and then by
	// sockets for tcp_host_ports.
	firstInterceptSocket
)

// The DNS server address in the sandbox resolv.conf (see
// jailfs.WriteEtcFiles) is local in the filtered mode, as all the IPv4
// addresses are.
const dnsPort = 53

// ListenFiltered sets up the sandbox network namespace for the
// filtered network mode and creates sockets that the parent serves:
//   - DNS server listening on UDP 0.0.0.0:53,
//   - for each port allowed by allowed_domains, a socket listening on
//     0.0.0.0:port that accepts intercepted connections,
//   - for each tcp_host_ports entry, a socket listening on
//     127.0.0.1:sandbox_port, connections to which are forwarded to the
//     host localhost port.
//
// The namespace has only the loopback interface, so nothing can leave
// the sandbox other than via the sockets served by the parent. All
// IPv4 addresses are made local, so a connection to any address is
// delivered to the listening sockets.
//
// Called by the child, requires CAP_NET_ADMIN and
// CAP_NET_BIND_SERVICE.
func ListenFiltered(netConfig config.Net) ([]*os.File, error) {
	ports, err := interceptedPorts(netConfig)
	if err != nil {
		return nil, err
	}
	if err := loopbackUp(); err != nil {
		return nil, err
	}
	if err := addLocalRoute(); err != nil {
		return nil, err
	}

	var files []*os.File
	closeAll := func() {
		for _, f := range files {
			f.Close()
		}
	}
	listen := func(network, address string) error {
		f, err := listenFile(network, address)
		if err != nil {
			return err
		}
		files = append(files, f)
		return nil
	}
	if err := listen("udp4", fmt.Sprintf("0.0.0.0:%d", dnsPort)); err != nil {
		return nil, err
	}
	for _, port := range ports {
		if err := listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port)); err != nil {
			closeAll()
			return nil, err
		}
	}
	for _, m := range netConfig.TCPHostPorts {
		if err := listen("tcp4", fmt.Sprintf("127.0.0.1:%d", m.GuestPort)); err != nil {
			closeAll()
			return nil, err
		}
	}
	return files, nil
}

// FilteredSocketCount returns the number of sockets returned by
// ListenFiltered.
func FilteredSocketCount(netConfig config.Net) (int, error) {
	ports, err := interceptedPorts(netConfig)
	if err != nil {
		return 0, err
	}
	return firstInterceptSocket + len(ports) + len(netConfig.TCPHostPorts), nil
}

// interceptedPorts returns ports on which connections are intercepted.
func interceptedPorts(netConfig config.Net) ([]int, error) {
	allow, err := netproxy.ParseAllowlist(netConfig.AllowedDomains)
	if err != nil {
		return nil, fmt.Errorf("invalid allowed_domains entry: %v", err)
	}
	return allow.Ports(), nil
}

// listenFile creates a listening socket and returns its file.
func listenFile(network, address string) (*os.File, error) {
	if network == "udp4" {
		pc, err := net.ListenPacket(network, address)
		if err != nil {
			return nil, fmt.Errorf("listen on %s %s: %v", network, address, err)
		}
		defer pc.Close()
		// File returns a duplicate of the socket.
		return pc.(*net.UDPConn).File()
	}
	l, err := net.Listen(network, address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s %s: %v", network, address, err)
	}
	defer l.Close()
	return l.(*net.TCPListener).File()
}

// loopbackUp brings up the loopback interface, which is down in a
// newly created network namespace.
func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("bring loopback up: create socket: %v", err)
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return fmt.Errorf("bring loopback up: %v", err)
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
		return fmt.Errorf("bring loopback up: get flags: %v", err)
	}
	ifr.SetUint16(ifr.Uint16() | unix.IFF_UP)
	if err := unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr); err != nil {
		return fmt.Errorf("bring loopback up: set flags: %v", err)
	}
	return nil
}

// addLocalRoute makes all IPv4 addresses local, equivalent to:
// ip route add local 0.0.0.0/0 dev lo table local
func addLocalRoute() error {
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		return fmt.Errorf("add local route: %v", err)
	}
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("add local route: create netlink socket: %v", err)
	}
	defer unix.Close(fd)

	// struct rtmsg followed by RTA_OIF attribute.
	rtmsg := []byte{
		unix.AF_INET, // rtm_family
		0,            // rtm_dst_len: 0.0.0.0/0
		0,            // rtm_src_len
		0,            // rtm_tos
		unix.RT_TABLE_LOCAL,
		unix.RTPROT_BOOT,
		unix.RT_SCOPE_HOST,
		unix.RTN_LOCAL,
		0, 0, 0, 0, // rtm_flags
	}
	attr := binary.NativeEndian.AppendUint16(nil, unix.SizeofRtAttr+4)
	attr = binary.NativeEndian.AppendUint16(attr, unix.RTA_OIF)
	attr = binary.NativeEndian.AppendUint32(attr, uint32(lo.Index))
	payload := append(rtmsg, attr...)

	msg := binary.NativeEndian.AppendUint32(nil, uint32(unix.NLMSG_HDRLEN+len(payload)))
	msg = binary.NativeEndian.AppendUint16(msg, unix.RTM_NEWROUTE)
	msg = binary.NativeEndian.AppendUint16(msg,
		unix.NLM_F_REQUEST|unix.NLM_F_ACK|unix.NLM_F_CREATE|unix.NLM_F_EXCL)
	msg = binary.NativeEndian.AppendUint32(msg, 1) // sequence number
	msg = binary.NativeEndian.AppendUint32(msg, 0) // port id
	msg = append(msg, payload...)
	if err := unix.Sendto(fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return fmt.Errorf("add local route: send: %v", err)
	}

	buf := make([]byte, 4096)
	n, _, err := unix.Recvfrom(fd, buf, 0)
	if err != nil {
		return fmt.Errorf("add local route: receive: %v", err)
	}
	msgs, err := syscall.ParseNetlinkMessage(buf[:n])
	if err != nil {
		return fmt.Errorf("add local route: parse response: %v", err)
	}
	for _, m := range msgs {
		if m.Header.Type != unix.NLMSG_ERROR || len(m.Data) < 4 {
			continue
		}
		// errno is negative, 0 means success (ACK).
		if errno := -int32(binary.NativeEndian.Uint32(m.Data)); errno != 0 {
			return fmt.Errorf("add local route: %v", syscall.Errno(errno))
		}
		return nil
	}
	return fmt.Errorf("add local route: no acknowledgment")
}

// StartProxy serves the filtering proxy on the sockets received from
// the child (see ListenFiltered). The parent process runs in the host
// network namespace, so the proxy connects to allowed destinations on
// behalf of the sandboxed processes. Requests are logged to logPath.
//
// Returns a cleanup function that should be called when program exits.
func StartProxy(files []*os.File, netConfig config.Net, logPath string) (func(), error) {
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	count, err := FilteredSocketCount(netConfig)
	if err != nil {
		return nil, err
	}
	if len(files) != count {
		return nil, fmt.Errorf("expected %d proxy sockets, got %d", count, len(files))
	}
	allow, err := netproxy.ParseAllowlist(netConfig.AllowedDomains)
	if err != nil {
		return nil, fmt.Errorf("invalid allowed_domains entry: %v", err)
	}

	// Sockets to close if starting the proxy fails.
	var sockets []io.Closer
	fail := func(err error) (func(), error) {
		for _, c := range sockets {
			c.Close()
		}
		return nil, err
	}
	dnsConn, err := net.FilePacketConn(files[dnsSocket])
	if err != nil {
		return fail(fmt.Errorf("proxy DNS socket: %v", err))
	}
	sockets = append(sockets, dnsConn)
	var listeners []net.Listener
	for _, f := range files[firstInterceptSocket:] {
		l, err := net.FileListener(f)
		if err != nil {
			return fail(fmt.Errorf("proxy listener: %v", err))
		}
		sockets = append(sockets, l)
		listeners = append(listeners, l)
	}
	interceptListeners := listeners[:len(listeners)-len(netConfig.TCPHostPorts)]
	hostPortListeners := listeners[len(interceptListeners):]

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fail(fmt.Errorf("open proxy log: %v", err))
	}
	proxy := netproxy.New(allow, logFile)
	go proxy.ServeDNS(dnsConn)
	for _, l := range interceptListeners {
		go proxy.ServeTransparent(l)
	}
	for i, l := range hostPortListeners {
		go proxy.ServeHostPort(l, netConfig.TCPHostPorts[i].HostPort)
	}
	return func() {
		proxy.Close()
		logFile.Close()
	}, nil
}
