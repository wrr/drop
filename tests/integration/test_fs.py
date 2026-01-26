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
import shutil

from contextlib import contextmanager
from pathlib import Path

import base

from base import TestBase, Config, ENV_DIR


HOME_DIR = Path.home()

class TestFS(TestBase):
    def test_home_dir_isolation(self):
        fname = 'test_file_foo_bar'
        cmd = f'bash -c "echo Hello world > ~/{fname}"'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)

        # Ensure the file was not created in the user home, but in the
        # jailed env home dir.
        self.assertFalse(os.path.exists(HOME_DIR / fname))
        jail_file = ENV_DIR / 'home' / fname
        self.assertTrue(os.path.exists(jail_file))

        self.assertEqual('Hello world\n', base.read(jail_file))

    def test_mount_read_only(self):
        exposed_dname = 'drop-test-data'
        home_sub_path = HOME_DIR / exposed_dname
        hello_path = home_sub_path / 'hello.txt'
        with scoped_dir(home_sub_path):
            base.write('hello', hello_path)
            config = Config(mounts=[f'~/{exposed_dname}'])
            # Reading from files in the dir exposed as readonly is
            # allowed
            cmd = f'cat {hello_path}'
            result = self.sandbox_run(cmd, config=config)
            self.assertSuccess(result)
            self.assertEqual('hello', result.stdout)

            # Writing to files in is not allowed
            cmd = f'bash -c "cat foo > {hello_path}"'
            result = self.sandbox_run(cmd, config=config)
            self.assertEqual(1, result.returncode)
            self.assertIn('Read-only file system', result.stderr)

            # Mount point should not be created directly in the Drop
            # home dir, but in a disposable overlayfs lower layer.
            self.assertFalse(os.path.exists(ENV_DIR / 'home' / exposed_dname))

    def test_cwd_mount_read_only(self):
        config = Config(cwd_mounts=['.'])
        # Reading from CWD should work
        result = self.sandbox_run('cat go.mod', config=config)
        self.assertSuccess(result)

        # Writing to CWD should fail
        result = self.sandbox_run('touch testfile', config=config)
        self.assertEqual(1, result.returncode)
        self.assertIn('Read-only file system', result.stderr)

    def test_cwd_blocked_path(self):
        config = Config(cwd_blocked_paths=['cmd'])
        result = self.sandbox_run('cat cmd/drop/main.go', config=config)
        self.assertEqual(1, result.returncode)
        self.assertIn('cmd/drop/main.go: Permission denied', result.stderr)

    def test_no_cwd_flag(self):
        """Test -no-cwd flag prevents CWD from being mounted"""
        # With cwd.mounts in config, CWD should be accessible by default
        config = Config(cwd_mounts=['.'])
        cwd = os.getcwd()
        result = self.sandbox_run(f'cat {cwd}/go.mod', config=config)
        self.assertSuccess(result)

        # With -no-cwd flag, CWD should not be accessible
        result = self.sandbox_run(f'cat {cwd}/go.mod', config=config,
                                  drop_extra_args='-no-cwd')
        self.assertEqual(1, result.returncode)
        self.assertIn('No such file or directory', result.stderr)

    def test_mounts_from_root_dir(self):
        # Expose a path outside of the home dir, normally not
        # available within Drop.
        config = Config(mounts=['/boot'])
        result = self.sandbox_run('ls /boot/', config=config)
        self.assertSuccess(result)

    def test_paths_remaping(self):
        # Mount /boot from host into the user homedir.
        config = Config(mounts=['/boot:~/boot'])
        result = self.sandbox_run('ls /boot/', config=config)
        self.assertEqual(2, result.returncode)
        self.assertIn("cannot access '/boot/': No such file or directory",
                      result.stderr)
        result = self.sandbox_run(f'ls {HOME_DIR / 'boot'}', config=config)
        self.assertSuccess(result)

    def test_mounts_target_is_file_not_a_dir(self):
        exposed_dname = 'drop-test-data'
        # Create an empty file in the env home dir that conflicts
        # with the exposed directory
        base.write('', ENV_DIR / 'home' / exposed_dname)

        host_dir = HOME_DIR / exposed_dname
        with scoped_dir(host_dir):
            config = Config(mounts=[f'~/{exposed_dname}'])
            # Should be the original file, not a dir
            result = self.sandbox_run('bash -c "test -f ~/drop-test-data"',
                                      config=config)
            self.assertEqual(0, result.returncode)
            expected_msg = (f"Drop: not mounting {host_dir}, target already "
                            "exists but is a file not a directory")
            self.assertIn(expected_msg, result.stderr)

    def test_mounts_target_is_dir_not_a_file(self):
        exposed_fname = 'drop-test-data'
        # Create a directory in the env home dir that conflicts
        # with exposed file.
        target_dir = ENV_DIR / 'home' / exposed_fname
        target_dir.mkdir(parents=True, exist_ok=True)

        host_file = HOME_DIR / exposed_fname
        with scoped_empty_file(host_file):
            config = Config(mounts=[f'~/{exposed_fname}'])
            # Should be the original dir, not a file
            result = self.sandbox_run('bash -c "test -d ~/drop-test-data"',
                                      config=config)
            self.assertEqual(0, result.returncode)
            expected_msg = (f"Drop: not mounting {host_file}, target already "
                            "exists but is a directory not a file")
            self.assertIn(expected_msg, result.stderr)

    def test_mounts_target_is_link(self):
        exposed_dname = 'drop-test-data'
        # Create a symlink in the env home directory
        target_link = ENV_DIR / 'home' / exposed_dname
        target_link.parent.mkdir(parents=True, exist_ok=True)
        target_link.symlink_to('/tmp')

        host_dir = HOME_DIR / exposed_dname
        with scoped_dir(host_dir):
            config = Config(mounts=[f'~/{exposed_dname}'])
            result = self.sandbox_run('bash -c "test -L ~/drop-test-data"',
                                      config=config)
            self.assertEqual(0, result.returncode)
            expected_msg = (f"Drop: not mounting {host_dir}, target already "
                            "exists but is a symbolic link")
            self.assertIn(expected_msg, result.stderr)

    def test_mounts_source_is_missing(self):
        exposed_dname = 'drop-test-data-missing'
        host_dir = HOME_DIR / exposed_dname
        if host_dir.exists():
            shutil.rmtree(host_dir)

        config = Config(mounts=[f'~/{exposed_dname}'])
        result = self.sandbox_run('bash -c "test -e ~/drop-test-data-missing"',
                                  config=config)
        # Exit 1, file should not exist
        self.assertEqual(1, result.returncode)
        expected_msg = (f"Drop: not mounting {host_dir}, "
                        "no such file or directory")
        self.assertIn(expected_msg, result.stderr)

    def test_mounts_validation(self):
        config = Config(mounts=['/etc/../usr'])
        result = self.sandbox_run('ls', config=config)
        self.assertEqual(1, result.returncode)
        self.assertIn(
            "invalid mounts '/etc/../usr': path is not normalized",
            result.stderr)

    def test_mount_read_write(self):
        exposed_dname = 'drop-test-data'
        home_sub_path = HOME_DIR / exposed_dname
        hello_path = home_sub_path / 'hello.txt'
        with scoped_dir(home_sub_path):
            base.write('hello', hello_path)
            config = Config(mounts=[f"~/{exposed_dname}::rw"])
            # Reading from files in the dir exposed in readwrite mode
            # is allowed
            cmd = f'cat {hello_path}'
            result = self.sandbox_run(cmd, config=config)
            self.assertSuccess(result)
            self.assertEqual('hello', result.stdout)

            # Writing to files is allowed, creating new files and dirs
            # is also allowed.
            cmd = (f'bash -c "'
                   f'echo world > {hello_path}; '
                   f'mkdir {home_sub_path}/foo; '
                   f'touch {home_sub_path}/bar; '
                   f'cat {hello_path};"')
            result = self.sandbox_run(cmd, config=config)
            self.assertSuccess(result)
            self.assertEqual('world\n', result.stdout)
            self.assertEqual('world\n', base.read(hello_path))
            self.assertTrue(os.path.isdir(home_sub_path / 'foo'))
            self.assertTrue(os.path.isfile(home_sub_path / 'bar'))
        # Mount point should not be created directly in the Drop
        # home dir, but in a disposable overlayfs lower layer.
        self.assertFalse(os.path.exists(ENV_DIR / 'home' / exposed_dname))

    def test_add_mount_flag(self):
        """Test -m flags"""
        dir1 = 'drop-test-dir1'
        dir2 = 'drop-test-dir2'
        path1 = HOME_DIR / dir1
        path2 = HOME_DIR / dir2
        file1 = path1 / 'file1.txt'
        file2 = path2 / 'file2.txt'

        with scoped_dir(path1), scoped_dir(path2):
            base.write('hello', file1)
            base.write('world', file2)

            drop_extra_args = f'-mount ~/{dir1} -m ~/{dir2}::rw'

            cmd = f'bash -c "cat {file1}; cat {file2}"'
            result = self.sandbox_run(cmd, drop_extra_args=drop_extra_args)
            self.assertSuccess(result)
            self.assertEqual('helloworld', result.stdout)

            # dir1 is read-only, writing should fail
            cmd = f'bash -c "echo bye > {file1}"'
            result = self.sandbox_run(cmd, drop_extra_args=drop_extra_args)
            self.assertEqual(1, result.returncode)
            self.assertIn('Read-only file system', result.stderr)
            self.assertEqual('hello', base.read(file1))

            # dir2 is read-write, writing should succeed
            cmd = f'bash -c "echo -n bye > {file2}; cat {file2}"'
            result = self.sandbox_run(cmd, drop_extra_args=drop_extra_args)
            self.assertSuccess(result)
            self.assertEqual('bye', result.stdout)
            self.assertEqual('bye', base.read(file2))

    def test_var(self):
        # Test that /var directory is empty initially and files created
        # in it are stored in the Drop env /var subdirectory

        cmd = 'bash -c "ls -A /var | wc -l"'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)
        line_count = int(result.stdout.strip())
        self.assertEqual(0, line_count)

        cmd = 'bash -c "echo hello > /var/foo"'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)

        # Ensure the file was not created in the host /var, but in the
        # Drop env /var dir.
        host_var_file = Path('/var/foo')
        self.assertFalse(host_var_file.exists())
        jail_var_file = ENV_DIR / 'var' / 'foo'
        self.assertTrue(jail_var_file.exists())

        self.assertEqual('hello\n', base.read(jail_var_file))

    def test_run(self):
        # Test that /run directory is empty
        cmd = 'bash -c "ls -A /run | wc -l"'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)
        line_count = int(result.stdout.strip())
        self.assertEqual(0, line_count)

    def test_etc(self):
        # Test that /etc/resolv.conf contains the expected DNS configuration
        cmd = 'cat /etc/resolv.conf'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)
        self.assertIn('nameserver 169.254.1.1', result.stdout)

    def test_blocked_paths(self):
        # Test that blocked paths are inaccessible
        config = Config(blocked_paths=['/var', '/etc/passwd'])

        result = self.sandbox_run('ls /var', config=config)
        self.assertNotEqual(0, result.returncode)
        self.assertIn('Permission denied', result.stderr)

        result = self.sandbox_run('cat /etc/passwd', config=config)
        self.assertNotEqual(0, result.returncode)
        self.assertIn('Permission denied', result.stderr)

    def test_blocked_home_dir_path(self):
        exposed_dname = 'drop-test-data'
        blocked_dname = 'blocked'
        home_sub_path = HOME_DIR / exposed_dname
        blocked_path = home_sub_path / blocked_dname
        with scoped_dir(home_sub_path):
            os.mkdir(blocked_path)
            config = Config(
                mounts=[f'~/{exposed_dname}'],
                blocked_paths=[f'~/{exposed_dname}/{blocked_dname}'])
            result = self.sandbox_run(f'ls {blocked_path}', config=config)
            self.assertNotEqual(0, result.returncode)
            self.assertIn('Permission denied', result.stderr)

    def test_devices(self):
        # Ensure /dev/null can be written to but its size remains 0
        cmd = 'bash -c "echo foo > /dev/null && stat -c %s /dev/null"'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)
        stat_out = result.stdout.strip()
        self.assertEqual('0', stat_out)

        # Read 10 bytes from /dev/zero and count them
        cmd = 'bash -c "dd if=/dev/zero bs=10 count=1 2>/dev/null | wc -c"'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)
        wc_out = result.stdout.strip()
        self.assertEqual('10', wc_out)

        # Read 4 bytes from each of /dev/random and /dev/urandom and count them
        cmd = 'bash -c "head -c 4 -q /dev/random /dev/urandom|wc -c"'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)
        wc_out = result.stdout.strip()
        self.assertEqual('8', wc_out)

        # Ensure write to /dev/full returns an error
        result = self.sandbox_run('bash -c "echo foo > /dev/full"')
        wc_out = result.stdout.strip()
        self.assertIn('No space left on device', result.stderr)
        self.assertEqual(1, result.returncode)

        # In total, expect only 13 entries in the jailed /dev dir (5
        # devices, 2 dirs, 6 links)
        cmd = 'bash -c "ls -1A /dev |wc -l"'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)
        stat_out = result.stdout.strip()
        self.assertEqual('13', stat_out)

    def test_blocked_fs_entries(self):
        """Test that always blocked paths are not readable"""
        blocked_entries = [
            '/proc/acpi',
            '/proc/asound',
            '/proc/kcore',
            '/proc/keys',
            '/proc/latency_stats',
            '/proc/timer_list',
            '/proc/scsi',
            # not always present:
            #'timer_stats',
            #'sched_debug',
            '/sys/firmware/',
        ]

        for path in blocked_entries:
            with self.subTest(path=path):
                # When path is a dir cat still produces Permission
                # denied
                result = self.sandbox_run(f'cat {path}')
                self.assertEqual(1, result.returncode)
                self.assertIn('Permission denied', result.stderr)

                result = self.sandbox_run(f'chmod 644 {path}')
                self.assertEqual(1, result.returncode)
                self.assertIn('Read-only file system', result.stderr)

    def test_not_exposed_root_sub_dirs(self):
        # These dirs are not exposed to Drop by default
        not_exposed_dirs = ['/root', '/boot', '/snap']

        for path in not_exposed_dirs:
            with self.subTest(path=path):
                result = self.sandbox_run(f'ls {path}')
                self.assertEqual(2, result.returncode)
                self.assertIn('No such file or directory', result.stderr)

    def test_snap_mounts_hidden(self):
        """Test that not needed host mounts are not visible in sandbox"""
        result = self.sandbox_run('cat /proc/self/mounts')
        self.assertSuccess(result)

        mount_lines = result.stdout.strip().split('\n')
        snap_mounts = [line for line in mount_lines if ' /snap/' in line]

        self.assertEqual(0, len(snap_mounts),
                         f'Unexpected /snap/ mounts in sandbox: {snap_mounts}')


@contextmanager
def scoped_dir(path):
    """Create directory and clean up afterwards."""
    try:
        path.mkdir(parents=True, exist_ok=True)
        yield path
    finally:
        if path.exists():
            shutil.rmtree(path)

@contextmanager
def scoped_empty_file(path):
    """Create an empty file and clean up afterwards."""
    try:
        base.write('', path)
        yield path
    finally:
        if path.exists():
            path.unlink()
