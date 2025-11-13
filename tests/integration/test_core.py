import getpass
import os
import re

import base

from base import Config, ENV_DIR

class TestCore(base.TestBase):
    def test_exit_code_passed(self):
        cmd = 'bash -c "exit 77"'
        result = self.sandbox_run(cmd)
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
        result = self.sandbox_run('bash -c "id -u; id -g"')
        self.assertSuccess(result)
        jail_uid, jail_gid = uid_gid_from_stdout()
        self.assertEqual(uid, jail_uid)
        self.assertEqual(gid, jail_gid)

        # In root mode (-r flag), Drop should use UID/GID 0
        result = self.sandbox_run('bash -c "id -u; id -g"',
                                  drop_extra_args='-r')
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
            result = self.sandbox_run('ls', env_id=None, cwd=cwd)
            self.assertSuccess(result)
            self.assertFalse(os.path.exists(ENV_DIR))
            self.assertTrue(os.path.exists(env_dir_from_cwd), 
                            f'env dir is missing {env_dir_from_cwd}')
        finally:
            base.rmdir(env_dir_from_cwd)

    def test_process_isolation(self):
        cmd = 'bash -c "sleep 10 & ps aux --noheaders"'
        result = self.sandbox_run(cmd)
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

    def test_exposed_env_vars(self):
        os.environ['FOO'] = 'bar'

        try:
            # Env variables should not be automatically exposed.
            cmd = 'bash -c "echo $FOO"'
            result = self.sandbox_run(cmd)
            self.assertSuccess(result)
            self.assertEqual('', result.stdout.strip())

            config = Config(exposed_env_vars=['FOO'])
            result = self.sandbox_run(cmd, config=config)
            self.assertSuccess(result)
            self.assertEqual('bar', result.stdout.strip())

        finally:
            del os.environ['FOO']

    def test_drop_env_set(self):
        """Test that DROP_ENV is set correctly"""
        cmd = 'bash -c "echo $DROP_ENV"'
        result = self.sandbox_run(cmd)
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
            result =  self.sandbox_run('ls /proc/1/fd/', pass_fds=pass_fds)
            self.assertSuccess(result)
            fds = [int(fd) for fd in result.stdout.splitlines()]
            # Except 4 open FDs, because one is used by 'ls' to read
            # content of the /proc/1/fd dir
            self.assertEqual([0, 1, 2, 3], fds)
        finally:
            for fd in pass_fds:
                os.close(fd)


