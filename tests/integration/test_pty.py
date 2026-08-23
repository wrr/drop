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

import functools
import os
import select
import unittest

from base import TestBase
from pathlib import Path


class Pty:
    """A pseudoterminal allocated for a test.

    parent is the manager fd (read/write it to drive the terminal).
    child is the subsidiary fd (pass it as a subprocess's stdin,
    stdout and/or stderr).
    """

    def __init__(self):
        self.parent, self.child = os.openpty()

    def read(self):
        """Returns the output currently available on the parent side as
        a string without blocking.
        """
        chunks = []
        while select.select([self.parent], [], [], 0)[0]:
            try:
                chunk = os.read(self.parent, 4096)
            except OSError:
                # the child side is closed
                break
            if not chunk:
                break
            chunks.append(chunk)
        return b''.join(chunks).decode(errors='replace')

    def close(self):
        for fd in (self.parent, self.child):
            try:
                os.close(fd)
            except OSError:
                pass


def allocate_pty(test_func):
    """Test decorator that allocates a Pty and passes it as a single
    argument to the test, closing it when the test finishes."""
    @functools.wraps(test_func)
    def wrapper(self, *args, **kwargs):
        pty = Pty()
        try:
            return test_func(self, pty, *args, **kwargs)
        finally:
            pty.close()
    return wrapper


class TestPty(TestBase):

    @allocate_pty
    def test_has_terminal(self, pty):
        self.drop_init()
        # pass terminal as stdin, then tty should be allocated in the sandbox
        result = self.drop_run('tty', stdin=pty.child)
        self.assertSuccess(result)
        self.assertEqual('/dev/pts/0', result.stdout.strip())

        # processes should have a controlling terminal, reported in ps
        # output (if process has not controlling terminal, ps reports
        # ? as the TTY)
        result = self.drop_run('ps -o tty=', stdin=pty.child)
        self.assertSuccess(result)
        self.assertEqual('pts/0', result.stdout.strip())

    @allocate_pty
    def test_exit_code_passed_with_terminal(self, pty):
        self.drop_init()
        result = self.drop_run('bash -c "exit 77"', stdin=pty.child)
        self.assertEqual('', result.stderr)
        self.assertEqual(77, result.returncode)

    @allocate_pty
    def test_run_dir_cleaned_up_after_terminal_session(self, pty):
        self.drop_init()
        # gVisor runtime allocates own pty only if all three file
        # descriptors point to a terminal
        result = self.drop_run('true', stdin=pty.child, stdout=pty.child,
                               stderr=pty.child)
        self.assertSuccess(result)

        run_dir = Path(self.drop_home) / 'internal' / 'run'
        leftovers = list(run_dir.iterdir()) if run_dir.exists() else []
        self.assertEqual([], leftovers)

    def test_no_terminal_when_streams_redirected(self):
        self.drop_init()
        # When all 3 streams are not terminals, tty should not be
        # allocated in the sanbox
        tty_result = self.drop_run('tty')
        ps_result = self.drop_run('ps -o tty=')

        # tty returns exit code 1 when not connected to a terminal
        self.assertEqual(1, tty_result.returncode)
        self.assertEqual('not a tty', tty_result.stdout.strip())

        # No controlling terminal
        self.assertSuccess(ps_result)
        self.assertEqual('?', ps_result.stdout.strip())

    @allocate_pty
    def test_only_terminal_fds_are_terminals_in_sandbox(self, pty):
        self.drop_init()
        result = self.drop_run('readlink /proc/self/fd/0',
                               stdin=pty.child)
        self.assertSuccess(result)
        self.assertEqual('/dev/pts/0', result.stdout.strip())

        # Pipes should not go through terminal
        result = self.drop_run('readlink /proc/self/fd/1')
        self.assertSuccess(result)
        self.assertIn('pipe:', result.stdout.strip())

        result = self.drop_run('readlink /proc/self/fd/2')
        self.assertSuccess(result)
        self.assertIn('pipe:', result.stdout.strip())

    def test_ptmx_cannot_be_removed(self):
        self.drop_init()
        # Even though /dev/ptmx is owned by the current user,
        # kernel should not allow to remove it.
        result = self.drop_run('rm -rf /dev/ptmx')
        self.assertEqual(1, result.returncode)
        # 'Device or resource busy' with native runtime, 'Permission
        # denied' with gVisor.
        self.assertRegex(result.stderr.strip(),
                         r'Device or resource busy|Permission denied')

    @allocate_pty
    def test_dev_tty(self, pty):
        self.drop_init()
        # /dev/tty must be the char device with major:minor 5:0.
        result = self.drop_run('stat -c %t:%T /dev/tty')
        self.assertSuccess(result)
        self.assertEqual('5:0', result.stdout.strip())

        # Writing to /dev/tty with a controlling terminal should succeed
        result = self.drop_run('bash -c "echo hello > /dev/tty"',
                               stdin=pty.child,
                               stdout=pty.child,
                               stderr=pty.child)
        self.assertEqual(0, result.returncode)
        self.assertEqual('hello', pty.read().strip())

        # Writing to /dev/pty without controlling terminal should fail
        result = self.drop_run('bash -c "echo hello > /dev/tty"')
        self.assertNotEqual(0, result.returncode)
        self.assertIn('/dev/tty: No such device or address',
                      result.stderr.strip())

    @allocate_pty
    def test_dev_tty_with_piped_stdin(self, pty):
        self.drop_init()
        # Equivalent of: echo 'x' | drop run sh -c "echo hello > /dev/tty"
        # stdin is a pipe while stdout/stderr are the terminal (mixed fds).
        stdin_r, stdin_w = os.pipe()
        os.write(stdin_w, b'x\n')
        os.close(stdin_w)
        try:
            result = self.drop_run('sh -c "echo hello > /dev/tty"',
                                   stdin=stdin_r,
                                   stdout=pty.child,
                                   stderr=pty.child)
        finally:
            os.close(stdin_r)
        self.assertEqual(0, result.returncode)
        self.assertEqual('hello', pty.read().strip())

    @allocate_pty
    def test_long_env_id(self, pty):
        # A regression test, long env id used to cause problems
        # because of a console socket path being longer than allowed
        # 107 characters.
        env_id = 'x' * 120
        self.drop_init(env_id)
        result = self.drop_run('true', env_id=env_id, stdin=pty.child)
        self.assertSuccess(result)


class TestPtyGvisor(TestPty):
    runtime = 'gvisor'

    @unittest.skip('terminal reporting not working with gVisor')
    def test_has_terminal(self):
        # See https://github.com/google/gvisor/issues/13535
        pass

    @allocate_pty
    def test_only_terminal_fds_are_terminals_in_sandbox(self, pty):
        # Unlike native runtime, gVisor will allocate terminal in the
        # sandbox only if all three fds are terminals. When some
        # non-terminal descriptors are passed to the sandbox, terminal
        # is not allocted within gVisor, but passed to it from Drop
        # (such terminal doesn't have /dev/tty entry).
        #
        # Suprisingly, even when gVisor allocates terminal, readlink
        # still reports std descriptors to point to host:[1], maybe due
        # to https://github.com/google/gvisor/issues/13535
        self.drop_init()
        result = self.drop_run('sh -c "readlink /proc/self/fd/0; ' +
                               'readlink /proc/self/fd/1; ' +
                               'readlink /proc/self/fd/2"',
                               stdin=pty.child, stdout=pty.child,
                               stderr=pty.child)
        self.assertEqual(0, result.returncode)
        self.assertEqual('host:[1]\r\nhost:[1]\r\nhost:[1]',
                         pty.read().strip())

        # Pipes should not go through terminal
        result = self.drop_run('sh -c "readlink /proc/self/fd/0; ' +
                               'readlink /proc/self/fd/1; ' +
                               'readlink /proc/self/fd/2"')
        self.assertSuccess(result)
        self.assertEqual('host:[1]\nhost:[2]\nhost:[3]', result.stdout.strip())

    @allocate_pty
    def test_dev_tty_with_piped_stdin(self, pty):
        self.drop_init()
        # When only some std descriptors are terminals, gVisor does not
        # allocate the terminal itself; Drop allocates it and passes it in,
        # so the sandbox has no /dev/tty entry for it (see the comment in
        # gvisor.go). Opening /dev/tty therefore fails.
        stdin_r, stdin_w = os.pipe()
        os.write(stdin_w, b'x\n')
        os.close(stdin_w)
        try:
            result = self.drop_run('sh -c "echo hello > /dev/tty"',
                                   stdin=stdin_r,
                                   stdout=pty.child,
                                   stderr=pty.child)
        finally:
            os.close(stdin_r)
        self.assertNotEqual(0, result.returncode)
        self.assertIn('cannot create /dev/tty: No such device or address',
                      pty.read())

