# Copyright 2026 Jan Wrobel <jan@mixedbit.org>
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
import tempfile

from base import TestBase, ENV_ID


class TestInit(TestBase):

    def test_init_required(self):
        result = self.drop_run('ls')
        self.assertEqual(1, result.returncode)
        self.assertIn(f'Error: environment "{ENV_ID}" doesn\'t exist',
                      result.stderr)

    def test_init_creates_env_dir_and_config_files(self):
        # Test DROP_HOME should not have the default base.toml until
        # the first environment is created.
        base_config_path = self.base_config_path()
        self.assertFalse(os.path.exists(base_config_path))

        result = self.drop('init foo')
        self.assertEqual(0, result.returncode)
        lines = result.stderr.split('\n')
        self.assertIn('Wrote base Drop config to', lines[0])
        self.assertIn('Drop environment created with config at', lines[1])

        env_dir = self.env_dir('foo')
        self.assertTrue(os.path.exists(env_dir),
                        f'Env directory was not created: {env_dir}')
        env_config_path = self.env_config_path('foo')
        self.assertTrue(os.path.exists(env_config_path),
                        f'Env config was not created: {env_config_path}')
        self.assertTrue(os.path.exists(base_config_path),
                        f'Base config was not created: {base_config_path}')

    def test_init_fails_if_env_exists(self):
        result = self.drop('init foo')
        self.assertEqual(0, result.returncode)
        result = self.drop('init foo')
        self.assertEqual(1, result.returncode)
        self.assertIn("environment foo already exists",
                      result.stderr)

    def test_env_id_from_cwd(self):
        # If env id is not passed, it should be constructed from
        # current working dir.
        cwd = tempfile.mkdtemp(prefix='drop-tests')
        env_id = str(cwd).replace('/', '-').strip('-')
        env_dir_from_cwd = self.env_dir(env_id)

        result = self.drop('init', cwd=cwd)
        self.assertEqual(0, result.returncode)
        self.assertTrue(os.path.exists(env_dir_from_cwd),
                        f'env dir is missing {env_dir_from_cwd}')
        default_env_dir = self.env_dir(ENV_ID)
        self.assertFalse(os.path.exists(default_env_dir))

    def test_init_too_many_arguments(self):
        result = self.drop('init foo bar')
        self.assertEqual(1, result.returncode)
        self.assertEqual(f'Error: usage: drop init [env-id]\n',
                         result.stderr)
