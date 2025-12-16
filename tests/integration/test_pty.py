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

import sys
import tempfile
import unittest

from base import TestBase

class TestPty(TestBase):

    @classmethod
    def setUpClass(cls):
        if not sys.stdin.isatty():
            raise unittest.SkipTest(
                "PTY test cases must be run from a terminal, skipping")

    def test_has_terminal(self):
        # sandbox_run redirects stdout and stderr to a pipe, but stdin
        # is still a terminal, so tty should be allocated in the sandbox
        result = self.sandbox_run('tty')
        self.assertSuccess(result)
        self.assertEqual('/dev/pts/0', result.stdout.strip())

        # processes should have a controlling terminal, reported in ps
        # output (if process has not controlling terminal, ps reports
        # ? as the TTY)
        result = self.sandbox_run('ps -o tty=')
        self.assertSuccess(result)
        self.assertEqual('pts/0', result.stdout.strip())

    def test_no_terminal_when_streams_redirected(self):
        # When all 3 streams are not terminals, tty should not be
        # allocated in the sanbox
        with tempfile.NamedTemporaryFile() as stdin_f:
            tty_result = self.sandbox_run('tty', stdin=stdin_f)
            ps_result = self.sandbox_run('ps -o tty=', stdin=stdin_f)

        # tty returns exit code 1 when not connected to a terminal
        self.assertEqual(1, tty_result.returncode)
        self.assertEqual('not a tty', tty_result.stdout.strip())

        # No controlling terminal
        self.assertSuccess(ps_result)
        self.assertEqual('?', ps_result.stdout.strip())

    def test_only_terminal_fds_are_terminals_in_sandbox(self):
        result = self.sandbox_run('readlink /proc/self/fd/0')
        self.assertSuccess(result)
        self.assertEqual('/dev/pts/0', result.stdout.strip())

        # Pipes should not go through terminal
        result = self.sandbox_run('readlink /proc/self/fd/1')
        self.assertSuccess(result)
        self.assertIn('pipe:', result.stdout.strip())

        result = self.sandbox_run('readlink /proc/self/fd/2')
        self.assertSuccess(result)
        self.assertIn('pipe:', result.stdout.strip())

    def test_ptmx_cannot_be_removed(self):
        # Even though /dev/ptmx is owned by the current user, 
        # kernel should not allow to remove it.
        result = self.sandbox_run('rm -rf /dev/ptmx')
        self.assertEqual(1, result.returncode)
        self.assertIn('Device or resource busy', result.stderr.strip())
