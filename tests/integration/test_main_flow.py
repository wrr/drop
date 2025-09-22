import getpass
import os
import re
import shutil
import subprocess
import tempfile
import unittest

JAIL_ID = 'integration-tests'
HOME_DIR = os.path.expanduser('~')
JAIL_DIR = os.path.join(HOME_DIR, '.dirjail', 'jails', JAIL_ID)

CONFIG = """
home_visible = []
home_writeable = []
proc_readable = []
hide = []
env_expose = []
"""

class TestMainFlow(unittest.TestCase):
    def setUp(self):
        remove_test_jail_dir()
        self.temp_dir = tempfile.mkdtemp()

    def tearDown(self):
        remove_test_jail_dir()
        if hasattr(self, 'temp_dir') and os.path.exists(self.temp_dir):
            shutil.rmtree(self.temp_dir)

    def sandbox_run(self, command):
        """Execute a command in the sandbox and return its result."""
        config_file = os.path.join(self.temp_dir, 'config.toml')
        with open(config_file, 'w') as f:
            f.write(CONFIG)
        return subprocess.run(
            f'./dirjail -c {config_file} -i {JAIL_ID} {command}',
            shell=True, capture_output=True, text=True)

    def test_process_list(self):
        cmd = 'ps aux --noheaders'
        result = self.sandbox_run(cmd)
        self.assertEqual('', result.stderr)
        self.assertEqual(0, result.returncode)
        ps_lines = result.stdout.strip().split('\n')
        self.assertEqual(2, len(ps_lines))

        user = getpass.getuser()
        init_process = rf'^{user}\s+1\s+.*dirjail.*'
        self.assertTrue(re.match(init_process, ps_lines[0]),
                        f"Unexpected ps output: {ps_lines[0]}")

        ps_process = rf'^{user}\s+\d+\s+.*{re.escape(cmd)}.*'
        self.assertTrue(re.match(ps_process, ps_lines[1]),
                        f"Unexpected ps output: {ps_lines[1]}")

        self.assertTrue(os.path.exists(JAIL_DIR),
                        f"Jail directory was not created: {JAIL_DIR}")




def remove_test_jail_dir():
    if os.path.exists(JAIL_DIR):
        shutil.rmtree(JAIL_DIR)
