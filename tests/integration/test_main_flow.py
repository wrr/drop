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

JAIL_ID = 'drop-tests'
HOME_DIR = Path.home()

def jail_dir(jail_id: str) -> Path:
    return HOME_DIR / '.dirjail' / 'jails' / jail_id

JAIL_DIR = jail_dir(JAIL_ID)


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
        rmdir(JAIL_DIR)
        self.temp_dir = tempfile.mkdtemp(prefix='drop-tests')

    def tearDown(self):
        rmdir(JAIL_DIR)
        if hasattr(self, 'temp_dir') and os.path.exists(self.temp_dir):
            shutil.rmtree(self.temp_dir)

    def sandbox_run(self, command, config: Config = None,
                    jail_id: str = JAIL_ID, cwd=None):
        """Execute a command in the sandbox and return its result."""
        if config is None:
            config = Config()
        config_file = os.path.join(self.temp_dir, 'config.toml')
        write_config(config, config_file)
        cmd_args = [f'{os.getcwd()}/dirjail', '-c', config_file]
        if jail_id:
            cmd_args += ['-i', jail_id]
        cmd_args += shlex.split(command)
        return subprocess.run(cmd_args, capture_output=True, text=True,
                              cwd=cwd)

    def assertSuccess(self, result):
        self.assertTrue(result.stderr == '',
                        f'Unexpected error {result.stderr}')
        self.assertEqual(0, result.returncode)

    def test_exit_code_passed(self):
        cmd = 'bash -c "exit 77"'
        result = self.sandbox_run(cmd)
        self.assertEqual('', result.stderr)
        self.assertEqual(77, result.returncode)
        self.assertTrue(os.path.exists(JAIL_DIR),
                        f'Jail directory was not created: {JAIL_DIR}')

    def test_jail_id_from_cwd(self):
        # If jail id is not passed, it should be constructed from
        # current working dir.
        cwd = self.temp_dir
        jail_id = str(cwd).replace('/', '-').strip('-')
        jail_dir_from_cwd = jail_dir(jail_id)
        rmdir(jail_dir_from_cwd)
        try:
            # Expose the directory where test coverage data is stored,
            # otherwise the test fails in coverage gathering mode.
            cover_path = Path(os.getcwd()) /  'cover'
            cover_path = cover_path.relative_to(Path.home())
            config = Config(home_writeable=[str(cover_path)])
            result = self.sandbox_run('ls', config=config, jail_id=None,
                                      cwd=cwd)
            self.assertSuccess(result)
            self.assertFalse(os.path.exists(JAIL_DIR))
            self.assertTrue(os.path.exists(jail_dir_from_cwd), 
                            f'jail dir is missing {jail_dir_from_cwd}')
        finally:
            pass
            rmdir(jail_dir_from_cwd)

    def test_process_isolation(self):
        cmd = 'ps aux --noheaders'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)

        # expect two processes in the sandbox
        ps_lines = result.stdout.strip().split('\n')
        self.assertEqual(2, len(ps_lines))

        # the first process should be init (pid 1)
        user = getpass.getuser()
        init_process = rf'^{user}\s+1\s+.*dirjail.*'
        self.assertTrue(re.match(init_process, ps_lines[0]),
                        f'Unexpected ps output: {ps_lines[0]}')

        # the second should be 'ps aux ...'
        ps_process = rf'^{user}\s+\d+\s+.*{re.escape(cmd)}.*'
        self.assertTrue(re.match(ps_process, ps_lines[1]),
                        f'Unexpected ps output: {ps_lines[1]}')

    def test_home_dir_isolation(self):
        fname = 'test_file_foo_bar'
        cmd = f'bash -c "echo Hello world > ~/{fname}"'
        result = self.sandbox_run(cmd)
        self.assertSuccess(result)

        # Ensure the file was not created in the user home, but in the
        # jail home dir.
        self.assertFalse(os.path.exists(HOME_DIR / fname))
        jail_file = JAIL_DIR / 'home' / fname
        self.assertTrue(os.path.exists(jail_file))

        self.assertEqual('Hello world\n', read(jail_file))

    def test_home_visible(self):
        exposed_dname = 'dirjail-test-data'
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
        exposed_dname = 'dirjail-test-data'
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
