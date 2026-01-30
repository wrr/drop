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

import getpass
import os
import re
import tempfile

import base

from base import Config, ENV_DIR


class TestCore(base.TestBase):
    def test_exit_code_passed(self):
        result = self.drop('run bash -c "exit 77"')
        self.assertEqual('', result.stderr)
        self.assertEqual(77, result.returncode)
        self.assertTrue(os.path.exists(ENV_DIR),
                        f'Env directory was not created: {ENV_DIR}')

    def test_uid_gid_mapping(self):
        def uid_gid_from_stdout():
            return map(int, result.stdout.strip().split('\n'))

        uid = os.getuid()
        gid = os.getgid()

        # Drop should preserve current user UID/GID
        result = self.drop('run bash -c "id -u; id -g"')
        self.assertSuccess(result)
        jail_uid, jail_gid = uid_gid_from_stdout()
        self.assertEqual(uid, jail_uid)
        self.assertEqual(gid, jail_gid)

        # In root mode (-r flag), Drop should use UID/GID 0
        result = self.drop('run -r bash -c "id -u; id -g"')
        self.assertSuccess(result)
        jail_uid, jail_gid = uid_gid_from_stdout()
        self.assertEqual(0, jail_uid),
        self.assertEqual(0, jail_gid)

    def test_env_id_from_cwd(self):
        # If env id is not passed, it should be constructed from
        # current working dir.
        cwd = self.temp_dir
        env_id = str(cwd).replace('/', '-').strip('-')
        env_dir_from_cwd = base.env_dir(env_id)
        base.rmdir(env_dir_from_cwd)
        try:
            result = self.drop('run ls', env_id=None, cwd=cwd)
            self.assertSuccess(result)
            self.assertFalse(os.path.exists(ENV_DIR))
            self.assertTrue(os.path.exists(env_dir_from_cwd), 
                            f'env dir is missing {env_dir_from_cwd}')
        finally:
            self.rm_env(env_id)

    def test_process_isolation(self):
        result = self.drop('run bash -c "sleep 10 & ps aux --noheaders"')
        self.assertSuccess(result)

        # expect three processes in the sandbox
        ps_lines = result.stdout.strip().split('\n')
        self.assertEqual(3, len(ps_lines))

        # the first process should be bash - the init process with
        # pid 1.
        user = getpass.getuser()
        init_process = rf'^{user}\s+1\s+.*bash.*'
        self.assertTrue(re.match(init_process, ps_lines[0]),
                        f'Unexpected ps output: {ps_lines[0]}')

        # the second should be 'sleep'
        ps_process = rf'^{user}\s+\d+\s+.*sleep.*'
        self.assertTrue(re.match(ps_process, ps_lines[1]),
                        f'Unexpected ps output: {ps_lines[1]}')

        # the third should be 'ps aux ...'
        ps_process = rf'^{user}\s+\d+\s+.*ps aux --noheaders.*'
        self.assertTrue(re.match(ps_process, ps_lines[2]),
                        f'Unexpected ps output: {ps_lines[2]}')

    def test_environ_exposed_vars(self):
        os.environ['FOO'] = 'bar'

        try:
            # Env variables should not be automatically exposed.
            cmd = 'run bash -c "echo $FOO"'
            result = self.drop(cmd)
            self.assertSuccess(result)
            self.assertEqual('', result.stdout.strip())

            config = Config(environ_exposed_vars=['FOO'])
            result = self.drop(cmd, config=config)
            self.assertSuccess(result)
            self.assertEqual('bar', result.stdout.strip())

        finally:
            del os.environ['FOO']

    def test_environ_set_vars(self):
        os.environ['FOO'] = 'bar'

        try:
            config = Config(environ_set_vars=['FOO=baz'])
            result = self.drop('run bash -c "echo $FOO"', config=config)
            self.assertSuccess(result)
            self.assertEqual('baz', result.stdout.strip())

        finally:
            del os.environ['FOO']

    def test_drop_env_set(self):
        """Test that DROP_ENV is set correctly"""
        result = self.drop('run bash -c "echo $DROP_ENV"')
        self.assertSuccess(result)

        drop_env = result.stdout.strip()
        self.assertEqual(base.ENV_ID, drop_env)

    def test_open_fds_not_passed_to_sanbox(self):
        try:
            # Start Drop with many additional open file descriptors,
            # sandboxed process should not have access to these file
            # descriptors, but only to stdin, stdout and stderr.
            pass_fds = []
            for x in range(10):
                pass_fds.extend(os.pipe())
            result =  self.drop('run ls /proc/1/fd/', pass_fds=pass_fds)
            self.assertSuccess(result)
            fds = [int(fd) for fd in result.stdout.splitlines()]
            # Except 4 open FDs, because one is used by 'ls' to read
            # content of the /proc/1/fd dir
            self.assertEqual([0, 1, 2, 3], fds)
        finally:
            for fd in pass_fds:
                os.close(fd)

    def test_change_drop_home_dir(self):
        """Test that DROP_HOME env var is respected"""
        drop_home = tempfile.mkdtemp(prefix='drop-home-test-')
        result = self.drop('run ls', drop_home=drop_home)
        self.assertSuccess(result)

        # Verify env dir was created in custom DROP_HOME
        expected_env_dir = os.path.join(drop_home, 'envs', base.ENV_ID)
        self.assertTrue(os.path.exists(expected_env_dir),
                        f'Drop env was not created in {expected_env_dir}')

        # Verify nothing was created in default ~/.drop
        self.assertFalse(os.path.exists(ENV_DIR),
                         f'Drop env should not exist in {ENV_DIR}')

    def test_list_environments(self):
        """Test listing Drop environments with -ls flag"""
        drop_home = tempfile.mkdtemp(prefix='drop-home-test-')
        result = self.drop('ls', drop_home=drop_home)
        self.assertSuccess(result)
        self.assertEqual('', result.stdout.strip())

        env_ids = ['env1', 'env2', 'env3']
        for env_id in env_ids:
            result = self.drop('run true', env_id=env_id, drop_home=drop_home)
            self.assertSuccess(result)

        result = self.drop('ls', drop_home=drop_home)
        self.assertSuccess(result)

        listed_envs = result.stdout.strip().split('\n')
        self.assertCountEqual(env_ids, listed_envs)

    def test_list_environments_rejects_args(self):
        """Test that 'drop ls' rejects trailing arguments"""
        result = self.drop('ls foo')
        self.assertNotEqual(0, result.returncode)
        self.assertIn('usage: drop ls', result.stderr)

    def test_cannot_overwrite_input_files_passed_via_std_streams(self):
        """Test that sandboxed process cannot modify files passed via stdin.

        When a file is passed to drop via stream redirection, a sandboxed
        process should not be able to modify the original file by writing
        to /proc/self/fd/N.
        """
        original_content = b'original'
        with tempfile.NamedTemporaryFile() as input_file:
            input_file.write(original_content)
            input_file.seek(0)

            # Try to overwrite the file via /proc/self/fd/0 from inside sandbox
            result = self.drop(
                'run bash -c "echo -n modified > /proc/self/fd/0"',
                stdin=input_file)
            self.assertSuccess(result)

            # The write should either fail or be redirected elsewhere.
            # The original file must remain unchanged.
            input_file.seek(0)
            actual_content = input_file.read()

        self.assertEqual(original_content, actual_content)

    def test_remove_environment(self):
        """Test removing Drop environments with rm subcommand"""
        drop_home = tempfile.mkdtemp(prefix='drop-home-test-')

        env_ids = ['env1', 'env2', 'env3']
        for env_id in env_ids:
            result = self.drop('run true', env_id=env_id, drop_home=drop_home)
            self.assertSuccess(result)

        result = self.drop('ls', drop_home=drop_home)
        self.assertSuccess(result)
        listed_envs = result.stdout.strip().split('\n')
        self.assertCountEqual(env_ids, listed_envs)

        result = self.drop('rm env1', drop_home=drop_home)
        self.assertSuccess(result)

        result = self.drop('ls', drop_home=drop_home)
        self.assertSuccess(result)
        listed_envs = result.stdout.strip().split('\n')
        self.assertCountEqual(['env2', 'env3'], listed_envs)

        # Test removing non-existent environment
        result = self.drop('rm missing', drop_home=drop_home)
        self.assertEqual(1, result.returncode)
        self.assertIn('environment does not exist', result.stderr)

        # Test removing environment with running instance
        # Start a background Drop instance in env2
        process = self.drop_background('run sleep 60', env_id='env2',
                                       drop_home=drop_home)
        result = self.drop('rm env2', drop_home=drop_home)
        self.assertEqual(1, result.returncode)
        self.assertIn('environment is used by running drop instances',
                      result.stderr.lower())

        # Clean up the background process
        self.kill_process(process)
        # After killing the process, removal should work
        result = self.drop('rm env2', drop_home=drop_home)
        self.assertSuccess(result)

        # Verify only env3 remains
        result = self.drop('ls', drop_home=drop_home)
        self.assertSuccess(result)
        self.assertEqual('env3', result.stdout.strip())


