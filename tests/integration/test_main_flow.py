import getpass
import os
import re
import shlex
import shutil
import subprocess
import tempfile
import unittest

from contextlib import contextmanager
from pathlib import Path
from typing import List

ENV_ID = 'drop-tests'
HOME_DIR = Path.home()

def env_dir(env_id: str) -> Path:
    return HOME_DIR / '.drop' / 'envs' / env_id

ENV_DIR = env_dir(ENV_ID)


class Config:
    def __init__(self, *,
                 home_visible: List[str] = None,
                 home_writeable: List[str] = None,
                 proc_readable: List[str] = None,
                 hide: List[str] = None,
                 env_expose: List[str] = None):
        self.home_visible = home_visible or []
        self.home_writeable = home_writeable or []
        self.proc_readable = proc_readable or []
        self.hide = hide or []
        self.env_expose = env_expose or []

    def toml(self) -> str:
        """Return configuration as TOML string"""
        toml_lines = [
            f'home_visible = {str(self.home_visible)}',
            f'home_writeable = {str(self.home_writeable)}',
            f'proc_readable = {str(self.proc_readable)}',
            f'hide = {str(self.hide)}',
            f'env_expose = {str(self.env_expose)}'
        ]
        return '\n'.join(toml_lines)


class TestMainFlow(unittest.TestCase):
    def setUp(self):
        rmdir(ENV_DIR)
        self.temp_dir = tempfile.mkdtemp(prefix='drop-tests')

    def tearDown(self):
        rmdir(ENV_DIR)
        if hasattr(self, 'temp_dir') and os.path.exists(self.temp_dir):
            shutil.rmtree(self.temp_dir)

    def sandbox_run(self, command, config: Config = None,
                    env_id: str = ENV_ID, drop_extra_args=None,
                    **subprocess_kwargs):
        """Execute a command in the sandbox and return its result."""
        if config is None:
            config = Config()
        config_file = os.path.join(self.temp_dir, 'config.toml')
        write_config(config, config_file)
        cmd_args = [f'{os.getcwd()}/drop']
        if drop_extra_args:
            cmd_args += shlex.split(drop_extra_args)
        cmd_args += ['-c', config_file]
        if env_id:
            cmd_args += ['-e', env_id]
        cmd_args += shlex.split(command)
        return subprocess.run(cmd_args, capture_output=True, text=True,
                              **subprocess_kwargs)

    def assertSuccess(self, result):
        self.assertTrue(result.stderr == '',
                        f'Unexpected error {result.stderr}')
        self.assertEqual(0, result.returncode)

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

        # In root mode (-r flag), drop should use UID/GID 0
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
        env_dir_from_cwd = env_dir(env_id)
        rmdir(env_dir_from_cwd)
        try:
            # Expose the directory where test coverage data is stored,
            # otherwise the test fails in coverage gathering mode.
            cover_path = Path(os.getcwd()) /  'cover'
            cover_path = cover_path.relative_to(Path.home())
            config = Config(home_writeable=[str(cover_path)])
            result = self.sandbox_run('ls', config=config, env_id=None,
                                      cwd=cwd)
            self.assertSuccess(result)
            self.assertFalse(os.path.exists(ENV_DIR))
            self.assertTrue(os.path.exists(env_dir_from_cwd), 
                            f'env dir is missing {env_dir_from_cwd}')
        finally:
            pass
            rmdir(env_dir_from_cwd)

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

        self.assertEqual('Hello world\n', read(jail_file))

    def test_home_visible(self):
        exposed_dname = 'drop-test-data'
        home_sub_path = HOME_DIR / exposed_dname
        hello_path = home_sub_path / 'hello.txt'
        with scoped_dir(home_sub_path):
            write('hello', hello_path)
            config = Config(home_visible=[exposed_dname])
            # Reading from files in home_visible dir is allowed
            cmd = f'cat {hello_path}'
            result = self.sandbox_run(cmd, config=config)
            self.assertSuccess(result)
            self.assertEqual('hello', result.stdout)

            # Writing to files in home_visible dir is not allowed
            cmd = f'bash -c "cat foo > {hello_path}"'
            result = self.sandbox_run(cmd, config=config)
            self.assertEqual(1, result.returncode)
            self.assertIn('Read-only file system', result.stderr)

    def test_home_writeable(self):
        exposed_dname = 'drop-test-data'
        home_sub_path = HOME_DIR / exposed_dname
        hello_path = home_sub_path / 'hello.txt'
        with scoped_dir(home_sub_path):
            write('hello', hello_path)
            config = Config(home_writeable=[exposed_dname])
            # Reading from files in home_writeable dir is allowed
            cmd = f'cat {hello_path}'
            result = self.sandbox_run(cmd, config=config)
            self.assertSuccess(result)
            self.assertEqual('hello', result.stdout)

            # Writing to files in home_writeable dir is allowed,
            # Creating new files and dirs is also allowed.
            cmd = (f'bash -c "'
                   f'echo world > {hello_path}; '
                   f'mkdir {home_sub_path}/foo; '
                   f'touch {home_sub_path}/bar; '
                   f'cat {hello_path};"')
            result = self.sandbox_run(cmd, config=config)
            self.assertSuccess(result)
            self.assertEqual('world\n', result.stdout)
            self.assertEqual('world\n', read(hello_path))
            self.assertTrue(os.path.isdir(home_sub_path / 'foo'))
            self.assertTrue(os.path.isfile(home_sub_path / 'bar'))

    def test_env_expose(self):
        os.environ['FOO'] = 'bar'

        try:
            # Env variables should not be automatically exposed.
            cmd = 'bash -c "echo $FOO"'
            result = self.sandbox_run(cmd)
            self.assertSuccess(result)
            self.assertEqual('', result.stdout.strip())

            config = Config(env_expose=['FOO'])
            result = self.sandbox_run(cmd, config=config)
            self.assertSuccess(result)
            self.assertEqual('bar', result.stdout.strip())

        finally:
            del os.environ['FOO']

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


    def test_open_fds_not_passed_to_sanbox(self):
        try:
            # Start drop with many additional open file descriptors,
            # sandboxed process should not have access to these file
            # descriptors, but only to stdin, stdout and stderr.
            pass_fds = []
            for x in range(10):
                pass_fds.extend(os.pipe())
            result =  self.sandbox_run('ls /proc/1/fd', pass_fds=pass_fds)
            self.assertSuccess(result)
            fds = [int(fd) for fd in result.stdout.splitlines()]
            # Except 4 open FDs, because one is used by 'ls' to read
            # content of the /proc/1/fd dir
            self.assertEqual([0, 1, 2, 3], fds)
        finally:
            for fd in pass_fds:
                os.close(fd)

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



@contextmanager
def scoped_dir(path):
    """Create directory and clean up afterwards."""
    try:
        path.mkdir(parents=True, exist_ok=True)
        yield path
    finally:
        if path.exists():
            shutil.rmtree(path)

def rmdir(path):
    if os.path.exists(path):
        shutil.rmtree(path)

def write(content, path: str) -> None:
    """Write content to a file"""
    with open(path, 'w') as f:
        f.write(content)

def write_config(config: Config, path: str) -> None:
    """Write a Config object to a file as TOML"""
    write(config.toml(), path)

def read(path: str) -> str:
    with open(path, 'r') as f:
        return f.read()
