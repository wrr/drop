# Copyright 2025 Jan Wrobel <jan@mixedbit.org>
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http:#www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import os
import select
import socket

from base import TestBase, Config


class TestNet(TestBase):

    def test_networking(self):
        # External connection to IP address
        result = self.drop('run nc -zv -w 1 1.1.1.1 80')
        self.assertEqual(0, result.returncode)
        self.assertIn('succeeded', result.stderr)

        # External connection with DNS resolution
        result = self.drop('run nc -zv -w 1 google.com 80')
        self.assertEqual(0, result.returncode)
        self.assertIn('succeeded', result.stderr)

        # No external connections allowed when run with '-n off'
        # option
        result = self.drop('run -n off nc -zv -w 1 1.1.1.1 80')
        self.assertEqual(1, result.returncode)
        self.assertIn('Network is unreachable', result.stderr)

        result = self.drop('run -n off nc -zv -w 1 google.com 80')
        self.assertEqual(1, result.returncode)
        self.assertIn('getaddrinfo for host', result.stderr)

    def test_pasta_not_found_error(self):
        # Ensure a helpful error message is shown when pasta binary is
        # not found. Clear the PATH to make pasta unavailable.
        env = os.environ.copy()
        env['PATH'] = ''
        result = self.drop('run ls', env=env)
        self.assertEqual(1, result.returncode)
        self.assertIn(
            'pasta binary for isolated networking not found', result.stderr)
        self.assertIn('sudo apt-get install passt', result.stderr)
        self.assertIn(
            'https://passt.top/passt/about/#availability', result.stderr)

    def test_port_publish(self):
        # Publish TCP port 20112 from the sandbox
        tcp_server = self.drop_background(
            'run bash -c "echo -n "hello" | nc -4 -v -l -p 20112"',
            config=Config(tcp_published_ports=['20112']),
        )
        self.wait_port_bound(tcp_server, 20112)
        response = loopback_read_tcp(20112)
        self.assertEqual('hello', response)
        # not self.assertSuccess becauce netcat prints connection
        # information to stderr in verbose mode.
        result = self.wait_process_completed(tcp_server)
        self.assertEqual(0, result.returncode)

        # publish UDP port 20112 from the sandbox
        udp_server = self.drop_background(
            'run bash -c "echo -n "hello" | nc -4 -v -W 1 -u -l -p 20112"',
            config=Config(udp_published_ports=['20112']),
        )
        self.wait_port_bound(udp_server, 20112)
        response = loopback_read_udp(20112)
        self.assertEqual('hello', response)
        self.wait_process_completed(udp_server)
        self.assertEqual(0, result.returncode)

    def test_port_not_published(self):
        # Port 20114 is open, but not published from the sandbox
        tcp_server = self.drop_background(
            'run bash -c "echo -n "hello" | nc -4 -v -l -p 20114"')
        self.wait_port_bound(tcp_server, 20114)
        # Attempt to connect should fail with ConnectionRefusedError
        with self.assertRaises((ConnectionRefusedError, OSError)):
            loopback_read_tcp(20114)
        self.kill_process(tcp_server)

        # Same scenarion for UDP
        udp_server = self.drop_background(
            'run bash -c "echo -n "hello" | nc -4 -v -W 1 -u -l -p 20114"',
        )
        self.wait_port_bound(udp_server, 20114)
        with self.assertRaises((socket.timeout, OSError)):
            loopback_read_udp(20114)
        self.kill_process(udp_server)

    def test_port_forwarding_from_host(self):
        # expose TCP port 20113 from the host to the sandbox
        tcp_server = self.run_background(
            'bash -c "echo -n hello | nc -4 -v -l -p 20113"'
        )
        self.wait_port_bound(tcp_server, 20113)
        result = self.drop(
            'run bash -c "nc -4 -w 1 127.0.0.1 20113"',
            config=Config(tcp_host_ports=['20113']),
        )
        self.assertSuccess(result)
        self.assertEqual('hello', result.stdout)
        self.kill_process(tcp_server)

        # Same scenario for UDP
        udp_server = self.run_background(
            'bash -c "echo -n hello | nc -4 -v -W 1 -u -l -p 20113"'
        )
        self.wait_port_bound(udp_server, 20113)
        result = self.drop(
            'run bash -c "echo -n test | nc -4 -w 1 -u 127.0.0.1 20113"',
            config=Config(udp_host_ports=['20113']),
        )
        self.assertSuccess(result)
        self.assertEqual('hello', result.stdout)
        self.kill_process(udp_server)

    def test_port_not_exposed_from_host(self):
        # Port 20115 is open, but not exposed from the host to the
        # sanbox
        tcp_server = self.run_background(
            'bash -c "echo -n "hello" | nc -4 -v -l -p 20115"')
        self.wait_port_bound(tcp_server, 20115)
        result = self.drop(
            'run bash -c "nc -4 -v -w 1 127.0.0.1 20115"')
        self.assertEqual(1, result.returncode)
        self.assertIn('Connection refused', result.stderr)
        self.kill_process(tcp_server)

    def test_port_flags_validation(self):
        test_cases = [
            {
                'args': '-t foo',
                'expected': ('Error: command line --tcp-publish flag: '
                             'invalid port number \'foo\'')
            },
            {
                'args': '-T 0',
                'expected': ('Error: command line --tcp-host flag: '
                             'port number out of range: 0')
            },
            {
                'args': '-u auto -u 8080',
                'expected': ('Error: command line flags: '
                             'invalid udp_published_ports: '
                             '"auto" must be the only '
                             'published port entry')
            },
            {
                'args': '-U foo',
                'expected': ('Error: command line --udp-host flag: '
                             'invalid port number \'foo\'')
            }
        ]

        for tc in test_cases:
            args = tc['args']
            with self.subTest(args=args):
                result = self.drop(f'run {args} ls')
                self.assertNotEqual(0, result.returncode,
                                   f"Expected failure for {tc['args']}")
                self.assertIn(tc['expected'], result.stderr)

    def wait_port_bound(self, process, port):
        """Wait for netcat to bind to a port by checking its stderr
        output in verbose mode.

        Args:
            process: Background process running netcat with -v flag
            port: Expected port number
        """
        timeout_sec = 3.0
        # With UDP port netcat verbose output starts with 'Bound on'
        # with TCP port 'Listening on'
        expected_output = rf'(Bound|Listening) on 0\.0\.0\.0 {port}'

        ready, _, _ = select.select([process.stderr], [], [], timeout_sec)
        if not ready:
            raise TimeoutError(f'Timeout waiting for port {port} to be bound')
        line = process.stderr.readline()
        self.assertRegex(line, expected_output,
                         f'Unrecognized netcat output: {line}')

def loopback_read_tcp(tcp_port):
    """Attempt to connect to a TCP port and return the response string."""
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        sock.settimeout(1.0)
        sock.connect(('127.0.0.1', tcp_port))
        return sock.recv(1024).decode('utf-8')
    finally:
        sock.close()

def loopback_read_udp(udp_port):
    """Attempt to send UDP data to a port and return the response string."""
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        sock.settimeout(1.0)
        # Send data first so server knows where to respond
        sock.sendto(b'test\n', ('127.0.0.1', udp_port))
        response, addr = sock.recvfrom(1024)
        return response.decode('utf-8')
    finally:
        sock.close()
