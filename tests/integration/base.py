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
import shlex
import shutil
import subprocess
import stat
import tempfile
import unittest

from pathlib import Path
from typing import List

ENV_ID = 'drop-tests'

class Config:
    def __init__(self, *,
                 mounts: List[str] = None,
                 blocked_paths: List[str] = None,
                 environ_exposed_vars: List[str] = None,
                 environ_set_vars: List[str] = None,
                 tcp_published_ports: List[str] = None,
                 tcp_host_ports: List[str] = None,
                 udp_published_ports: List[str] = None,
                 udp_host_ports: List[str] = None):
        self.mounts = mounts or []
        # Always expose the directory where test coverage data is
        # stored, to ensure all tests can write coverage data.
        cover_path = Path(os.getcwd()) /  'cover'
        self.mounts += [str(cover_path) + "::rw"]
        self.blocked_paths = blocked_paths or []
        self.environ_exposed_vars = environ_exposed_vars or []
        self.environ_set_vars = environ_set_vars or []
        self.tcp_published_ports = tcp_published_ports or []
        self.tcp_host_ports = tcp_host_ports or []
        self.udp_published_ports = udp_published_ports or []
        self.udp_host_ports = udp_host_ports or []

    def toml(self) -> str:
        """Return configuration as TOML string"""
        toml_lines = [
            f'mounts = {str(self.mounts)}',
            f'blocked_paths = {str(self.blocked_paths)}',
            f'environ.exposed_vars = {str(self.environ_exposed_vars)}',
            f'environ.set_vars = {str(self.environ_set_vars)}',
            '',
            '[net]',
            f'tcp_published_ports = {str(self.tcp_published_ports)}',
            f'tcp_host_ports = {str(self.tcp_host_ports)}',
            f'udp_published_ports = {str(self.udp_published_ports)}',
            f'udp_host_ports = {str(self.udp_host_ports)}'
        ]
        return '\n'.join(toml_lines)


class TestBase(unittest.TestCase):
    """Base class for Drop integration tests"""

    def setUp(self):
        self.drop_home = tempfile.mkdtemp(prefix='drop-tests')

        self.background_processes = []
        self.created_homes = set()
        self.created_homes.add(self.drop_home)

    def tearDown(self):
        for process in self.background_processes:
            if process.poll() is None:  # Process is still running
                process.kill()

        for drop_home in self.created_homes:
            rm_drop_home(drop_home)

    def env_dir(self, env_id=ENV_ID, drop_home=None):
        if not drop_home:
            drop_home = self.drop_home
        return Path(drop_home) / 'envs' / env_id

    def base_config_path(self, drop_home=None):
        if not drop_home:
            drop_home = self.drop_home
        return Path(drop_home) / 'config' / 'base.toml'

    def env_config_path(self, env_id=ENV_ID, drop_home=None):
        if not drop_home:
            drop_home = self.drop_home
        return Path(drop_home) / 'config' / f'{env_id}.toml'

    def run_background(self, command, **subprocess_kwargs):
        """Execute a background command.

        Does not wait for the command to finish execution.
        Returns Popen object.

        Args:
            command: Command string or list of arguments
        """
        if isinstance(command, str):
            cmd_args = shlex.split(command)
        else:
            cmd_args = command

        if 'stdin' not in subprocess_kwargs:
            subprocess_kwargs['stdin'] = subprocess.DEVNULL
        if 'stdout' not in subprocess_kwargs:
            subprocess_kwargs['stdout'] = subprocess.PIPE
        if 'stderr' not in subprocess_kwargs:
            subprocess_kwargs['stderr'] = subprocess.PIPE

        process = subprocess.Popen(cmd_args, text=True, **subprocess_kwargs)
        self.background_processes.append(process)
        return process

    def drop_background(self, command=None, drop_home: str = None,
                        **subprocess_kwargs):
        """Execute a background drop command.

        Does not wait for the command to finish execution.
        Returns Popen object.
        """

        env = subprocess_kwargs.get('env') or os.environ.copy()
        if drop_home:
            self.created_homes.add(drop_home)
        else:
            drop_home = self.drop_home
        env['DROP_HOME'] = drop_home
        subprocess_kwargs['env'] = env

        command = f'{os.getcwd()}/drop {command}'
        return self.run_background(command, **subprocess_kwargs)

    def drop(self, command: str=None, drop_home: str = None,
             **subprocess_kwargs):
        """Execute a drop command and return its result."""
        process = self.drop_background(
            command, drop_home, **subprocess_kwargs)
        return self.wait_process_completed(process)

    def drop_init(self, env_id: str = ENV_ID, args='',
                  drop_home: str = None) -> None:
        result = self.drop(f'init {env_id} {args}', drop_home=drop_home)
        self.assertEqual(
            0, result.returncode,
            f'Failed to create drop environment: {result.stderr}')

    def drop_run_background(self, args=None, config: Config = None,
                            drop_home: str = None, env_id: str = ENV_ID,
                            **subprocess_kwargs):
        """Execute a background 'drop run' command.

        Does not wait for the command to finish execution.
        Returns Popen object.
        """
        added_args = []
        if config is None:
            config = Config()
        config_file = os.path.join(self.drop_home, 'config', 'base.toml')
        write_config(config, config_file)
        if env_id is not None:
            added_args += ['-e', env_id]
        command = f'run {" ".join(added_args)} {args}'
        return self.drop_background(command, drop_home=drop_home,
                                    **subprocess_kwargs)

    def drop_run(self, args=None, config: Config = None,
                 drop_home: str = None, env_id: str = ENV_ID,
                 **subprocess_kwargs):
        """Execute a 'drop run' command and return its result."""
        process = self.drop_run_background(args, config, drop_home, env_id,
                                           **subprocess_kwargs)
        return self.wait_process_completed(process)

    def wait_process_completed(self, process):
        stdout, stderr = process.communicate(timeout=500)
        retcode = process.poll()
        return subprocess.CompletedProcess(
            returncode=retcode,
            args=process.args,
            stdout=stdout,
            stderr=stderr
        )

    def kill_process(self, process):
        """Kill a process and wait for it to exit"""
        process.kill()
        # We don't use process.communicate() here because for
        # uninvestigated reason it randomly timeouts if the process is
        # first killed.
        process.stdout.close()
        process.stderr.close()
        process.wait()

    def rm_env(self, env_id, drop_home=None):
        result = self.drop(f'rm {env_id}', drop_home=drop_home)

    def assertSuccess(self, result):
        self.assertTrue(not result.stderr, f'Unexpected error {result.stderr}')
        self.assertEqual(0, result.returncode)


def rmdir(path):
    if os.path.exists(path):
        shutil.rmtree(path)

def ensure_can_delete_tree(path):
    """Changes permissions so shutil.rmtree can delete the whole tree"""
    min_perm = 0o700
    grant_permissions(path, min_perm)
    for entry in os.scandir(path):
        grant_permissions(entry.path, min_perm)
        if entry.is_dir(follow_symlinks=False):
            ensure_can_delete_tree(entry.path)

def grant_permissions(path, perms):
    current = stat.S_IMODE(os.lstat(path).st_mode)
    if (current & perms) != perms:
        os.chmod(path, current | perms)

def rm_drop_home(drop_home):
    # Change permissions of directories with 000 permissions
    # (e.g. emptyd, overlayfs work dirs).
    ensure_can_delete_tree(drop_home)
    shutil.rmtree(drop_home)

def write(content, path: str) -> None:
    """Write content to a file, ensure parent dir exists"""
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    with open(path, 'w') as f:
        f.write(content)

def write_config(config: Config, path: str) -> None:
    """Write a Config object to a file as TOML"""
    write(config.toml(), path)

def read(path: str) -> str:
    with open(path, 'r') as f:
        return f.read()

