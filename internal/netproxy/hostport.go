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
	"fmt"
	"net"
	"time"
)

// ServeHostPort accepts connections on l (listening on a sandbox
// localhost port) and connects them to the host localhost hostPort
// until Close is called. This exposes services running on the host
// localhost to the sandbox (tcp_host_ports setting). The ports are
// configured explicitly, so allowed_domains and address checks don't
// apply.
func (p *Proxy) ServeHostPort(l net.Listener, hostPort int) error {
	if !p.addCloser(l) {
		return nil
	}
	target := fmt.Sprintf("127.0.0.1:%d", hostPort)
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	for {
		client, err := l.Accept()
		if err != nil {
			if p.isClosed() {
				return nil
			}
			p.logger.Printf("accept host port connection: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go func() {
			upstream, err := dialer.Dial("tcp4", target)
			if err != nil {
				p.logger.Printf("failed TCP host port %s: %v", target, err)
				reset(client)
				return
			}
			p.logger.Printf("allowed TCP host port %s", target)
			p.pipe(client, client, upstream)
		}()
	}
}
