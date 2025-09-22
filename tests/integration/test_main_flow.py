import getpass
import os
import re
import shutil
import subprocess
import tempfile
import unittest

from pathlib import Path
from typing import List

JAIL_ID = 'drop-tests'
HOME_DIR = Path.home()
JAIL_DIR = HOME_DIR / '.dirjail' / 'jails' / JAIL_ID

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
            f"home_visible = {str(self.home_visible)}",
            f"home_writeable = {str(self.home_writeable)}",
            f"proc_readable = {str(self.proc_readable)}",
            f"hide = {str(self.hide)}",
            f"env_expose = {str(self.env_expose)}"
        ]
        return '\n'.join(toml_lines)


def write_config(config: Config, path: str) -> None:
    """Write a Config object to a file as TOML."""
    with open(path, 'w') as f:
        f.write(config.toml())


class TestMainFlow(unittest.TestCase):
    def setUp(self):
        remove_test_jail_dir()
        self.temp_dir = tempfile.mkdtemp(prefix='drop-tests')

    def tearDown(self):
        remove_test_jail_dir()
        if hasattr(self, 'temp_dir') and os.path.exists(self.temp_dir):
            shutil.rmtree(self.temp_dir)

    def sandbox_run(self, command, config: Config = None):
        """Execute a command in the sandbox and return its result."""
        if config is None:
            config = Config()
        config_file = os.path.join(self.temp_dir, 'config.toml')
        write_config(config, config_file)
        return subprocess.run(
            f'./dirjail -c {config_file} -i {JAIL_ID} {command}',
            shell=True, capture_output=True, text=True)

    def assertSuccess(self, result):
        self.assertEqual('', result.stderr)
        self.assertEqual(0, result.returncode)

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

        # the second process should be 'ps aux ...'
        ps_process = rf'^{user}\s+\d+\s+.*{re.escape(cmd)}.*'
        self.assertTrue(re.match(ps_process, ps_lines[1]),
                        f'Unexpected ps output: {ps_lines[1]}')

        self.assertTrue(os.path.exists(JAIL_DIR),
                        f'Jail directory was not created: {JAIL_DIR}')

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

        with open(jail_file, 'r') as f:
            content = f.read()
        self.assertEqual('Hello world\n', content)


def remove_test_jail_dir():
    if os.path.exists(JAIL_DIR):
        shutil.rmtree(JAIL_DIR)
