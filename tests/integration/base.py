import os
import shlex
import shutil
import subprocess
import tempfile
import unittest

from pathlib import Path
from typing import List

ENV_ID = 'drop-tests'

def env_dir(env_id: str) -> Path:
    return Path.home() / '.drop' / 'envs' / env_id

ENV_DIR = env_dir(ENV_ID)

class Config:
    def __init__(self, *,
                 mounts: List[str] = None,
                 blocked_paths: List[str] = None,
                 cwd_mounts: List[str] = None,
                 cwd_blocked_paths: List[str] = None,
                 exposed_env_vars: List[str] = None,
                 tcp_publish: List[str] = None,
                 tcp_from_host: List[str] = None,
                 udp_publish: List[str] = None,
                 udp_from_host: List[str] = None):
        self.mounts = mounts or []
        # Always expose the directory where test coverage data is
        # stored, to ensure all tests can write coverage data.
        cover_path = Path(os.getcwd()) /  'cover'
        self.mounts += [str(cover_path) + "::rw"]
        self.blocked_paths = blocked_paths or []
        self.cwd_mounts = cwd_mounts or ['.::rw']
        self.cwd_blocked_paths = cwd_blocked_paths or []
        self.exposed_env_vars = exposed_env_vars or []
        self.tcp_publish = tcp_publish or []
        self.tcp_from_host = tcp_from_host or []
        self.udp_publish = udp_publish or []
        self.udp_from_host = udp_from_host or []

    def toml(self) -> str:
        """Return configuration as TOML string"""
        toml_lines = [
            f'mounts = {str(self.mounts)}',
            f'blocked_paths = {str(self.blocked_paths)}',
            f'cwd.mounts = {str(self.cwd_mounts)}',
            f'cwd.blocked_paths = {str(self.cwd_blocked_paths)}',
            f'exposed_env_vars = {str(self.exposed_env_vars)}',
            '',
            '[net]',
            f'tcp_publish = {str(self.tcp_publish)}',
            f'tcp_from_host = {str(self.tcp_from_host)}',
            f'udp_publish = {str(self.udp_publish)}',
            f'udp_from_host = {str(self.udp_from_host)}'
        ]
        return '\n'.join(toml_lines)


class TestBase(unittest.TestCase):
    """Base class for Drop integration tests"""

    def setUp(self):
        rmdir(ENV_DIR)
        self.temp_dir = tempfile.mkdtemp(prefix='drop-tests')
        self.background_processes = []

    def tearDown(self):
        for process in self.background_processes:
            if process.poll() is None:  # Process is still running
                process.kill()

        rmdir(ENV_DIR)
        if hasattr(self, 'temp_dir') and os.path.exists(self.temp_dir):
            shutil.rmtree(self.temp_dir)

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
        process = subprocess.Popen(cmd_args, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True,
                                   **subprocess_kwargs)
        self.background_processes.append(process)
        return process

    def sandbox_run_background(self, command, config: Config = None,
                               env_id: str = ENV_ID, drop_extra_args=None,
                               **subprocess_kwargs):
        """Execute a background command in the sandbox.

        Does not wait for the command to finish execution.
        Returns Popen object.
        """
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
        return self.run_background(cmd_args, **subprocess_kwargs)

    def sandbox_run(self, command, config: Config = None,
                    env_id: str = ENV_ID, drop_extra_args=None,
                    **subprocess_kwargs):
        """Execute a command in the sandbox and return its result."""
        process = self.sandbox_run_background(
            command, config, env_id, drop_extra_args, **subprocess_kwargs)
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

    def assertSuccess(self, result):
        self.assertTrue(result.stderr == '',
                        f'Unexpected error {result.stderr}')
        self.assertEqual(0, result.returncode)


def rmdir(path):
    if os.path.exists(path):
        shutil.rmtree(path)

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

