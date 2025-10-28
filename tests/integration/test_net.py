import os
import socket
import time

from base import TestBase, Config


class TestNet(TestBase):

    def test_networking(self):
        # External connection to IP address
        result = self.sandbox_run('nc -zv -w 1 1.1.1.1 80')
        self.assertEqual(0, result.returncode)
        self.assertIn('succeeded', result.stderr)

        # External connection with DNS resolution
        result = self.sandbox_run('nc -zv -w 1 google.com 80')
        self.assertEqual(0, result.returncode)
        self.assertIn('succeeded', result.stderr)

        # No external connections allowed when run with '-n off'
        # option
        result = self.sandbox_run('nc -zv -w 1 1.1.1.1 80',
                                  drop_extra_args='-n off')
        self.assertEqual(1, result.returncode)
        self.assertIn('Network is unreachable', result.stderr)

        result = self.sandbox_run('nc -zv -w 1 google.com 80',
                                  drop_extra_args='-n off')
        self.assertEqual(1, result.returncode)
        self.assertIn('Temporary failure in name resolution', result.stderr)

    def test_pasta_not_found_error(self):
        # Ensure a helpful error message is shown when pasta binary is
        # not found. Clear the PATH to make pasta unavailable.
        env = os.environ.copy()
        env['PATH'] = ''
        result = self.sandbox_run('ls', env=env)
        self.assertEqual(1, result.returncode)
        self.assertIn(
            'pasta binary for isolated networking not found', result.stderr)
        self.assertIn('sudo apt-get install passt', result.stderr)
        self.assertIn(
            'https://passt.top/passt/about/#availability', result.stderr)

    def test_port_forwarding(self):
        # expose TCP port 20112 from the sandbox to the host
        process = self.sandbox_run_background(
            'bash -c "echo -n "hello" | nc -4 -l -p 20112"',
            config=Config(tcp_ports_to_host=['20112']),
        )

        response = loopback_read_tcp(20112)
        self.assertEqual('hello', response)
        self.assertSuccess(self.wait_process_completed(process))

        # expose UDP port 20112 from the sandbox to the host
        process = self.sandbox_run_background(
            'bash -c "echo -n "hello" | nc -4 -W 1 -u -l -p 20112"',
            config=Config(udp_ports_to_host=['20112']),
        )

        response = loopback_read_udp(20112)
        self.assertEqual('hello', response)
        self.assertSuccess(self.wait_process_completed(process))

    def test_port_flags_validation(self):
        test_cases = [
            {
                'args': '-t foo',
                'expected': 'invalid -t flag: invalid port number \'foo\''
            },
            {
                'args': '-T 0',
                'expected': 'invalid -T flag: port number out of range: 0'
            },
            {
                'args': '-u auto -u 8080',
                'expected': ('invalid -u flag: "auto" must be the only '
                             'port forwarding rule')
            },
            {
                'args': '-U foo.ip/8080:80',
                'expected': ('invalid -U flag: invalid port forwarding '
                             'IP address: foo.ip')
            }
        ]

        for tc in test_cases:
            with self.subTest(args=tc['args']):
                result = self.sandbox_run('ls', drop_extra_args=tc['args'])
                self.assertNotEqual(0, result.returncode,
                                   f"Expected failure for {tc['args']}")
                self.assertIn(tc['expected'], result.stderr)


def loopback_read_tcp(tcp_port):
    """Attempt to connect to a TCP port and return the response string.

    Retries upto 7 times with exponential backoff.
    """
    def tcp_read(sock):
        sock.connect(('127.0.0.1', tcp_port))
        return sock.recv(1024).decode('utf-8')

    return socket_read(socket.SOCK_STREAM, tcp_read)

def loopback_read_udp(udp_port):
    """Attempt to send UDP data to a port and return the response string.

    Retries upto 7 times with exponential backoff.
    """
    def udp_read(sock):
        # Send data first so server knows where to respond
        sock.sendto(b'test\n', ('127.0.0.1', udp_port))
        response, addr = sock.recvfrom(1024)
        return response.decode('utf-8')

    return socket_read(socket.SOCK_DGRAM, udp_read)

def socket_read(socket_type, read_callback):
    """Generic socket read with retry logic and exponential backoff.

    Args:
        socket_type: socket.SOCK_STREAM or socket.SOCK_DGRAM
        read_callback: function that takes a socket and returns response string
    """
    retries_count = 7
    delay_sec = 0.05
    for retry in range(retries_count):
        sock = socket.socket(socket.AF_INET, socket_type)
        try:
            sock.settimeout(1.0)
            return read_callback(sock)
        except (ConnectionRefusedError, socket.timeout, OSError) as e:
            if retry == retries_count - 1:
                raise e
            time.sleep(delay_sec)
            # Retry with backoff
            delay_sec *= 2
        finally:
            sock.close()
